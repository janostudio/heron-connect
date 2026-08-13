package core

// engine_reply.go — outgoing message infrastructure.
//
// Covers:
//   - SendToSession, SendToSessionWithAttachments
//   - sendPermissionPrompt, sendAskQuestionPrompt
//   - waitOutgoing, renderOutgoingContentForWorkspace
//   - sendWithErrorForWorkspace, sendForWorkspace
//   - renderCardForPlatform, renderCardForPlatformWorkspace
//   - sendWithError, sendAlreadyRenderedWithError, send, sendRaw
//   - drainEvents
//   - replyWithError, reply, replyWithButtons, supportsCards, replyWithCard, sendWithCard
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)
func (e *Engine) SendToSession(sessionKey, message string) error {
	return e.SendToSessionWithAttachments(sessionKey, message, nil, nil)
}

func (e *Engine) SendToSessionWithAttachments(sessionKey, message string, images []ImageAttachment, files []FileAttachment) error {
	e.interactiveMu.Lock()

	var state *interactiveState
	if sessionKey != "" {
		state = e.interactiveStates[sessionKey]
		if state == nil && e.multiWorkspace {
			// We already hold interactiveMu, so call the *Locked variant
			// to avoid a self-deadlock on the non-reentrant mutex.
			if iKey := e.interactiveKeyForSessionKeyLocked(sessionKey); iKey != sessionKey {
				state = e.interactiveStates[iKey]
			}
		}
	} else if len(e.interactiveStates) == 1 {
		// Single session: use it when no sessionKey is provided (backward compatible)
		for _, s := range e.interactiveStates {
			state = s
			break
		}
	} else if len(e.interactiveStates) > 1 && (len(images) > 0 || len(files) > 0) {
		// Multiple sessions with attachments but no explicit sessionKey: ambiguous
		e.interactiveMu.Unlock()
		return fmt.Errorf("multiple active sessions; must specify --session to send attachments")
	} else {
		// Multiple sessions but text-only: pick the first (legacy behavior)
		for _, s := range e.interactiveStates {
			state = s
			break
		}
	}
	e.interactiveMu.Unlock()

	var p Platform
	var replyCtx any
	if state != nil {
		state.mu.Lock()
		p = state.platform
		replyCtx = state.replyCtx
		state.mu.Unlock()
	}

	if p == nil && sessionKey != "" {
		strippedKey := sessionKey
		platformName := ""
		if idx := strings.Index(strippedKey, ":"); idx > 0 {
			platformName = strippedKey[:idx]
		}
		var targetPlatform Platform
		for _, candidate := range e.platforms {
			if candidate.Name() == platformName {
				targetPlatform = candidate
				break
			}
		}
		// Fallback: multi-workspace mode may prefix the session key with the
		// workspace path (same heuristic as ExecuteCronJob / ExecuteHeartbeat).
		if targetPlatform == nil {
			for _, candidate := range e.platforms {
				needle := ":" + candidate.Name() + ":"
				if idx := strings.Index(strippedKey, needle); idx >= 0 {
					targetPlatform = candidate
					strippedKey = strippedKey[idx+1:]
					break
				}
			}
		}
		if targetPlatform != nil {
			rc, ok := targetPlatform.(ReplyContextReconstructor)
			if !ok {
				return fmt.Errorf("platform %q does not support proactive messaging", targetPlatform.Name())
			}
			reconstructed, err := rc.ReconstructReplyCtx(strippedKey)
			if err != nil {
				return fmt.Errorf("reconstruct reply context: %w", err)
			}
			p = targetPlatform
			replyCtx = reconstructed
		}
	}

	if p == nil {
		return fmt.Errorf("no active session found (key=%q)", sessionKey)
	}

	if message == "" && len(images) == 0 && len(files) == 0 {
		return fmt.Errorf("message or attachment is required")
	}
	if (len(images) > 0 || len(files) > 0) && !e.attachmentSendEnabled {
		return ErrAttachmentSendDisabled
	}

	var imageSender ImageSender
	if len(images) > 0 {
		var ok bool
		imageSender, ok = p.(ImageSender)
		if !ok {
			return fmt.Errorf("platform %s: %w", p.Name(), ErrNotSupported)
		}
	}

	var fileSender FileSender
	if len(files) > 0 {
		var ok bool
		fileSender, ok = p.(FileSender)
		if !ok {
			return fmt.Errorf("platform %s: %w", p.Name(), ErrNotSupported)
		}
	}

	if message != "" {
		if err := e.waitOutgoing(p); err != nil {
			return err
		}
		if err := p.Send(e.ctx, replyCtx, message); err != nil {
			return err
		}
		if state != nil {
			state.mu.Lock()
			state.sideText = strings.TrimSpace(message)
			state.mu.Unlock()
		}
	}
	for _, img := range images {
		if err := e.waitOutgoing(p); err != nil {
			return err
		}
		if err := imageSender.SendImage(e.ctx, replyCtx, img); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err := e.waitOutgoing(p); err != nil {
			return err
		}
		if err := fileSender.SendFile(e.ctx, replyCtx, file); err != nil {
			return err
		}
	}
	return nil
}

// sendPermissionPrompt sends a permission prompt with interactive buttons when
// the platform supports them. Fallback chain: InlineButtonSender → CardSender → plain text.
func (e *Engine) sendPermissionPrompt(p Platform, replyCtx any, prompt, toolName, toolInput string) {
	e.hooks.Emit(HookEvent{
		Event:    HookEventPermissionRequested,
		Platform: p.Name(),
		Content:  prompt,
		Extra:    map[string]any{"tool_name": toolName},
	})

	// Try inline buttons first (Telegram)
	if bs, ok := p.(InlineButtonSender); ok {
		buttons := [][]ButtonOption{
			{
				{Text: e.i18n.T(MsgPermBtnAllow), Data: "perm:allow"},
				{Text: e.i18n.T(MsgPermBtnDeny), Data: "perm:deny"},
			},
			{
				{Text: e.i18n.T(MsgPermBtnAllowAll), Data: "perm:allow_all"},
			},
		}
		if err := e.waitOutgoing(p); err != nil {
			slog.Warn("sendPermissionPrompt: outgoing wait cancelled", "platform", p.Name(), "error", err)
			return
		}
		if err := bs.SendWithButtons(e.ctx, replyCtx, prompt, buttons); err == nil {
			return
		} else {
			slog.Warn("sendPermissionPrompt: inline buttons failed, falling back", "error", err)
		}
	}

	// Try card with buttons (Feishu/Lark)
	if supportsCards(p) {
		body := fmt.Sprintf(e.i18n.T(MsgPermCardBody), toolName, toolInput)
		extra := func(label, color string) map[string]string {
			return map[string]string{
				"perm_label": label,
				"perm_color": color,
				"perm_body":  body,
			}
		}
		allowBtn := CardButton{Text: e.i18n.T(MsgPermBtnAllow), Type: "primary", Value: "perm:allow",
			Extra: extra("✅ "+e.i18n.T(MsgPermBtnAllow), "green")}
		denyBtn := CardButton{Text: e.i18n.T(MsgPermBtnDeny), Type: "danger", Value: "perm:deny",
			Extra: extra("❌ "+e.i18n.T(MsgPermBtnDeny), "red")}
		allowAllBtn := CardButton{Text: e.i18n.T(MsgPermBtnAllowAll), Type: "default", Value: "perm:allow_all",
			Extra: extra("✅ "+e.i18n.T(MsgPermBtnAllowAll), "green")}

		card := NewCard().
			Title(e.i18n.T(MsgPermCardTitle), "orange").
			Markdown(body).
			ButtonsEqual(allowBtn, denyBtn).
			Buttons(allowAllBtn).
			Note(e.i18n.T(MsgPermCardNote)).
			Build()
		e.sendWithCard(p, replyCtx, card)
		return
	}

	e.send(p, replyCtx, prompt)
}

// sendAskQuestionPrompt renders one question (by index) from the AskUserQuestion list.
// qIdx is the 0-based index of the question to display.
func (e *Engine) sendAskQuestionPrompt(p Platform, replyCtx any, questions []UserQuestion, qIdx int) {
	if qIdx >= len(questions) {
		return
	}
	q := questions[qIdx]
	total := len(questions)

	titleSuffix := ""
	if total > 1 {
		titleSuffix = fmt.Sprintf(" (%d/%d)", qIdx+1, total)
	}

	// Try card (Feishu/Lark)
	if supportsCards(p) {
		cb := NewCard().Title(e.i18n.T(MsgAskQuestionTitle)+titleSuffix, "blue")
		body := "**" + q.Question + "**"
		if q.MultiSelect {
			body += e.i18n.T(MsgAskQuestionMulti)
		}
		cb.Markdown(body)
		for i, opt := range q.Options {
			desc := opt.Label
			if opt.Description != "" {
				desc += " — " + opt.Description
			}
			answerData := fmt.Sprintf("askq:%d:%d", qIdx, i+1)
			cb.ListItemBtnExtra(desc, opt.Label, "default", answerData, map[string]string{
				"askq_label":    opt.Label,
				"askq_question": q.Question,
			})
		}
		cb.Note(e.i18n.T(MsgAskQuestionNote))
		e.sendWithCard(p, replyCtx, cb.Build())
		return
	}

	// Try inline buttons (Telegram)
	if bs, ok := p.(InlineButtonSender); ok {
		var textBuf strings.Builder
		textBuf.WriteString("❓ *")
		textBuf.WriteString(q.Question)
		textBuf.WriteString("*")
		textBuf.WriteString(titleSuffix)
		if q.MultiSelect {
			textBuf.WriteString(e.i18n.T(MsgAskQuestionMulti))
		}
		hasDesc := false
		for _, opt := range q.Options {
			if opt.Description != "" {
				hasDesc = true
				break
			}
		}
		if hasDesc {
			textBuf.WriteString("\n")
			for i, opt := range q.Options {
				textBuf.WriteString(fmt.Sprintf("\n*%d. %s*", i+1, opt.Label))
				if opt.Description != "" {
					textBuf.WriteString(" — ")
					textBuf.WriteString(opt.Description)
				}
			}
			textBuf.WriteString("\n")
		}
		var rows [][]ButtonOption
		for i, opt := range q.Options {
			rows = append(rows, []ButtonOption{{Text: opt.Label, Data: fmt.Sprintf("askq:%d:%d", qIdx, i+1)}})
		}
		if err := e.waitOutgoing(p); err != nil {
			slog.Warn("sendAskQuestionPrompt: outgoing wait cancelled", "platform", p.Name(), "error", err)
			return
		}
		if err := bs.SendWithButtons(e.ctx, replyCtx, textBuf.String(), rows); err == nil {
			return
		}
	}

	// Plain text fallback
	var sb strings.Builder
	sb.WriteString("❓ **")
	sb.WriteString(q.Question)
	sb.WriteString("**")
	sb.WriteString(titleSuffix)
	if q.MultiSelect {
		sb.WriteString(e.i18n.T(MsgAskQuestionMulti))
	}
	sb.WriteString("\n\n")
	for i, opt := range q.Options {
		sb.WriteString(fmt.Sprintf("%d. **%s**", i+1, opt.Label))
		if opt.Description != "" {
			sb.WriteString(" — ")
			sb.WriteString(opt.Description)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n%s", e.i18n.T(MsgAskQuestionNote)))
	e.send(p, replyCtx, sb.String())
}

// waitOutgoing blocks on the per-platform outgoing rate limiter when enabled.
func (e *Engine) waitOutgoing(p Platform) error {
	if e.outgoingRL == nil {
		return nil
	}
	return e.outgoingRL.Wait(e.ctx, p.Name())
}

func (e *Engine) renderOutgoingContentForWorkspace(p Platform, content, workspaceDir string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	return TransformLocalReferences(content, e.references, e.agent.Name(), p.Name(), workspaceDir)
}

func (e *Engine) sendWithErrorForWorkspace(p Platform, replyCtx any, content, workspaceDir string) error {
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return err
	}
	content = e.renderOutgoingContentForWorkspace(p, content, workspaceDir)
	return e.sendAlreadyRenderedWithError(p, replyCtx, content)
}

func (e *Engine) sendForWorkspace(p Platform, replyCtx any, content, workspaceDir string) {
	_ = e.sendWithErrorForWorkspace(p, replyCtx, content, workspaceDir)
}

func (e *Engine) renderCardForPlatform(p Platform, card *Card) *Card {
	return e.renderCardForPlatformWorkspace(p, card, "")
}

func (e *Engine) renderCardForPlatformWorkspace(p Platform, card *Card, workspaceDir string) *Card {
	if card == nil {
		return nil
	}
	out := &Card{}
	if card.Header != nil {
		h := *card.Header
		out.Header = &h
	}
	out.Elements = make([]CardElement, 0, len(card.Elements))
	for _, elem := range card.Elements {
		switch v := elem.(type) {
		case CardMarkdown:
			content := v.Content
			if workspaceDir != "" {
				content = e.renderOutgoingContentForWorkspace(p, v.Content, workspaceDir)
			}
			out.Elements = append(out.Elements, CardMarkdown{Content: content})
		case CardNote:
			text := v.Text
			if workspaceDir != "" {
				text = e.renderOutgoingContentForWorkspace(p, v.Text, workspaceDir)
			}
			out.Elements = append(out.Elements, CardNote{Text: text, Tag: v.Tag})
		case CardListItem:
			text := v.Text
			if workspaceDir != "" {
				text = e.renderOutgoingContentForWorkspace(p, v.Text, workspaceDir)
			}
			out.Elements = append(out.Elements, CardListItem{
				Text:     text,
				BtnText:  v.BtnText,
				BtnType:  v.BtnType,
				BtnValue: v.BtnValue,
				Extra:    v.Extra,
			})
		default:
			out.Elements = append(out.Elements, elem)
		}
	}
	return out
}

// sendWithError applies outgoing rate limiting and p.Send. It logs wait
// cancellation and platform failures, and returns a non-nil error on either.
func (e *Engine) sendWithError(p Platform, replyCtx any, content string) error {
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return err
	}
	return e.sendAlreadyRenderedWithError(p, replyCtx, content)
}

func (e *Engine) sendAlreadyRenderedWithError(p Platform, replyCtx any, content string) error {
	start := time.Now()
	if err := p.Send(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform send failed", "platform", p.Name(), "error", err, "content_len", len(content))
		return err
	}
	if elapsed := time.Since(start); elapsed >= slowPlatformSend {
		slog.Warn("slow platform send", "platform", p.Name(), "elapsed", elapsed, "content_len", len(content))
	}
	return nil
}

// send wraps p.Send with error logging, slow-operation warnings, and outgoing rate limiting.
func (e *Engine) send(p Platform, replyCtx any, content string) {
	_ = e.sendWithError(p, replyCtx, content)
}

// sendRaw sends content without local-reference rendering. This is used for raw
// tool outputs, where preserving the original text is preferable to applying the
// agent-facing reference display transform.
func (e *Engine) sendRaw(p Platform, replyCtx any, content string) {
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return
	}
	_ = e.sendAlreadyRenderedWithError(p, replyCtx, content)
}

// drainEvents discards any buffered events from the channel.
// Called before a new turn to prevent stale events from a previous turn's
// agent process from being mistaken for the new turn's response.
// Returns the number of dropped events for diagnostics.
func drainEvents(ch <-chan Event) int {
	drained := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				// Channel is closed; stop immediately to avoid an infinite loop.
				return drained
			}
			drained++
		default:
			if drained > 0 {
				slog.Warn("drained stale events from previous turn", "count", drained)
			}
			return drained
		}
	}
}

// replyWithError applies outgoing rate limiting and p.Reply.
func (e *Engine) replyWithError(p Platform, replyCtx any, content string) error {
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return err
	}
	start := time.Now()
	if err := p.Reply(e.ctx, replyCtx, content); err != nil {
		slog.Error("platform reply failed", "platform", p.Name(), "error", err, "content_len", len(content))
		return err
	}
	if elapsed := time.Since(start); elapsed >= slowPlatformSend {
		slog.Warn("slow platform reply", "platform", p.Name(), "elapsed", elapsed, "content_len", len(content))
	}
	return nil
}

// reply wraps p.Reply with error logging, slow-operation warnings, and outgoing rate limiting.
func (e *Engine) reply(p Platform, replyCtx any, content string) {
	_ = e.replyWithError(p, replyCtx, content)
}

// replyWithButtons sends a reply with inline buttons if the platform supports it,
// otherwise falls back to plain text reply.
func (e *Engine) replyWithButtons(p Platform, replyCtx any, content string, buttons [][]ButtonOption) {
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return
	}
	if bs, ok := p.(InlineButtonSender); ok {
		if err := bs.SendWithButtons(e.ctx, replyCtx, content, buttons); err == nil {
			return
		}
	}
	e.reply(p, replyCtx, content)
}

func supportsCards(p Platform) bool {
	_, ok := p.(CardSender)
	return ok
}

// replyWithCard sends a structured card via CardSender.
// For platforms without card support, renders as plain text (no intermediate fallback).
func (e *Engine) replyWithCard(p Platform, replyCtx any, card *Card) {
	if card == nil {
		slog.Error("replyWithCard: nil card", "platform", p.Name())
		return
	}
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return
	}
	if cs, ok := p.(CardSender); ok {
		rendered := e.renderCardForPlatform(p, card)
		if err := cs.ReplyCard(e.ctx, replyCtx, rendered); err != nil {
			slog.Error("card reply failed", "platform", p.Name(), "error", err)
		}
		return
	}
	e.reply(p, replyCtx, e.renderCardForPlatform(p, card).RenderText())
}

// sendWithCard sends a card as a new message (not a reply).
func (e *Engine) sendWithCard(p Platform, replyCtx any, card *Card) {
	if card == nil {
		slog.Error("sendWithCard: nil card", "platform", p.Name())
		return
	}
	if err := e.waitOutgoing(p); err != nil {
		slog.Warn("outgoing rate limit: context cancelled", "platform", p.Name(), "error", err)
		return
	}
	if cs, ok := p.(CardSender); ok {
		rendered := e.renderCardForPlatform(p, card)
		if err := cs.SendCard(e.ctx, replyCtx, rendered); err != nil {
			slog.Error("card send failed", "platform", p.Name(), "error", err)
		}
		return
	}
	e.send(p, replyCtx, e.renderCardForPlatform(p, card).RenderText())
}

// ──────────────────────────────────────────────────────────────
// Card navigation (in-place card updates)
// ──────────────────────────────────────────────────────────────

// handleCardNav is called by platforms that support in-place card updates.
// It routes nav: and act: prefixed actions to the appropriate render function.
