package core

// engine_relay.go — bot-to-bot relay logic.
//
// Covers:
//   - HandleRelay
//   - relayPartialResponseOrError
//   - drainRelaySession
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)
// ──────────────────────────────────────────────────────────────
// Bot-to-bot relay
// ──────────────────────────────────────────────────────────────

// HandleRelay processes a relay message synchronously: starts or resumes a
// dedicated relay session, sends the message to the agent, and blocks until
// the complete response is collected (or the relay context times out).
func (e *Engine) HandleRelay(ctx context.Context, fromProject, chatID, message string) (string, error) {
	relaySessionKey := "relay:" + fromProject + ":" + chatID
	session := e.sessions.GetOrCreateActive(relaySessionKey)

	if inj, ok := e.agent.(SessionEnvInjector); ok {
		envVars := []string{
			"CC_PROJECT=" + e.name,
			"CC_SESSION_KEY=" + relaySessionKey,
		}
		if exePath, err := os.Executable(); err == nil {
			binDir := filepath.Dir(exePath)
			if curPath := os.Getenv("PATH"); curPath != "" {
				envVars = append(envVars, "PATH="+binDir+string(filepath.ListSeparator)+curPath)
			}
		}
		inj.SetSessionEnv(envVars)
	}

	// Use the engine context (not the relay timeout context) so that the
	// agent process is not killed when the relay deadline fires. The relay
	// timeout only controls how long we *wait* for the response.
	agentSession, err := e.agent.StartSession(e.ctx, session.GetAgentSessionID())
	if err != nil {
		// Resume failed — fall back to a fresh session so the relay is not
		// permanently broken by a corrupted/stale session ID.
		if session.GetAgentSessionID() != "" {
			slog.Warn("relay: session resume failed, trying fresh session",
				"relay_key", relaySessionKey, "error", err)
			session.SetAgentSessionID("", e.agent.Name())
			e.sessions.Save()
			agentSession, err = e.agent.StartSession(e.ctx, "")
		}
		if err != nil {
			return "", fmt.Errorf("start relay session: %w", err)
		}
	}

	if newID := agentSession.CurrentSessionID(); newID != "" {
		if session.CompareAndSetAgentSessionID(newID, e.agent.Name()) {
			pendingName := session.GetName()
			if pendingName != "" && pendingName != "session" && pendingName != "default" {
				e.sessions.SetSessionName(newID, pendingName)
			}
			e.sessions.Save()
		}
	}

	if err := agentSession.Send(message, nil, nil); err != nil {
		agentSession.Close()
		return "", fmt.Errorf("send relay message: %w", err)
	}

	var textParts []string
	for event := range agentSession.Events() {
		switch event.Type {
		case EventText:
			if event.Content != "" {
				textParts = append(textParts, event.Content)
			}
			if event.SessionID != "" {
				if session.CompareAndSetAgentSessionID(event.SessionID, e.agent.Name()) {
					pendingName := session.GetName()
					if pendingName != "" && pendingName != "session" && pendingName != "default" {
						e.sessions.SetSessionName(event.SessionID, pendingName)
					}
					e.sessions.Save()
				}
			}
		case EventToolResult:
			out := strings.TrimSpace(event.Content)
			if out == "" {
				out = strings.TrimSpace(event.ToolResult)
			}
			if out != "" {
				tn := strings.TrimSpace(event.ToolName)
				if tn == "" {
					tn = "tool"
				}
				textParts = append(textParts, fmt.Sprintf(e.i18n.T(MsgToolResult), tn, out)+"\n\n")
			}
		case EventResult:
			// Use agentSession.CurrentSessionID() for the same reason as above.
			if currentID := agentSession.CurrentSessionID(); currentID != "" {
				if session.CompareAndSetAgentSessionID(currentID, e.agent.Name()) {
					pendingName := session.GetName()
					if pendingName != "" && pendingName != "session" && pendingName != "default" {
						e.sessions.SetSessionName(currentID, pendingName)
					}
				}
				e.sessions.Save()
			}
			resp := event.Content
			if resp == "" && len(textParts) > 0 {
				resp = strings.Join(textParts, "")
			}
			if resp == "" {
				resp = "(empty response)"
			}
			slog.Info("relay: turn complete", "from", fromProject, "to", e.name, "response_len", len(resp))
			agentSession.Close()
			return resp, nil
		case EventError:
			agentSession.Close()
			if event.Error != nil {
				return "", event.Error
			}
			return "", fmt.Errorf("agent error (no details)")
		case EventPermissionRequest:
			// Auto-approve all permissions in relay mode
			_ = agentSession.RespondPermission(event.RequestID, PermissionResult{
				Behavior:     "allow",
				UpdatedInput: event.ToolInputRaw,
			})
		}
		if ctx.Err() != nil {
			// Relay timed out. Let the agent finish its turn in the
			// background so the session state is saved cleanly and the
			// session remains resumable for the next relay call.
			go e.drainRelaySession(agentSession, session, relaySessionKey)
			return relayPartialResponseOrError(ctx.Err(), textParts, fromProject, e.name)
		}
	}

	// Event channel closed without EventResult.
	agentSession.Close()

	if ctx.Err() != nil {
		return relayPartialResponseOrError(ctx.Err(), textParts, fromProject, e.name)
	}

	if len(textParts) > 0 {
		return strings.Join(textParts, ""), nil
	}
	return "", fmt.Errorf("relay: agent process exited without response")
}

func relayPartialResponseOrError(ctxErr error, textParts []string, fromProject, toProject string) (string, error) {
	if len(textParts) == 0 {
		return "", ctxErr
	}

	resp := strings.Join(textParts, "")
	slog.Warn("relay: context done before final result; returning partial response",
		"from", fromProject,
		"to", toProject,
		"error", ctxErr,
		"response_len", len(resp),
	)
	return resp, nil
}

// drainRelaySession runs in a goroutine after a relay timeout. It lets the
// agent finish its current turn (saving the session ID for future resumption),
// auto-approves any permission requests, and then closes the session. A 10-minute
// safety timeout prevents the goroutine from leaking if the agent hangs.
func (e *Engine) drainRelaySession(agentSession AgentSession, session *Session, relaySessionKey string) {
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case ev, ok := <-agentSession.Events():
			if !ok {
				// Event channel closed — session ended naturally.
				agentSession.Close()
				return
			}
			if ev.SessionID != "" {
				session.SetAgentSessionID(ev.SessionID, e.agent.Name())
				e.sessions.Save()
			}
			switch ev.Type {
			case EventResult:
				slog.Info("relay: background drain completed (agent finished turn)",
					"relay_key", relaySessionKey)
				agentSession.Close()
				return
			case EventError:
				slog.Warn("relay: background drain got error",
					"relay_key", relaySessionKey, "error", ev.Error)
				agentSession.Close()
				return
			case EventPermissionRequest:
				_ = agentSession.RespondPermission(ev.RequestID, PermissionResult{
					Behavior:     "allow",
					UpdatedInput: ev.ToolInputRaw,
				})
			}
		case <-timer.C:
			slog.Warn("relay: background drain timed out, closing session",
				"relay_key", relaySessionKey)
			agentSession.Close()
			return
		case <-e.ctx.Done():
			agentSession.Close()
			return
		}
	}
}

// cmdBind handles /bind — establishes a relay binding between bots in a group chat.
//
// Usage:
//
//	/bind <project>           — bind current bot with another project in this group
//	/bind remove              — remove all bindings for this group
//	/bind -<project>          — remove specific project from binding
//	/bind                     — show current binding status
//
// The <project> argument is the project name from config.toml [[projects]].
// Multiple projects can be bound together for relay.
// ──────────────────────────────────────────────────────────────
// Bot-to-bot relay
// ──────────────────────────────────────────────────────────────

// HandleRelay processes a relay message synchronously: starts or resumes a
// dedicated relay session, sends the message to the agent, and blocks until
// the complete response is collected (or the relay context times out).
