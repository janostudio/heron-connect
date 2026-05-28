package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/gorilla/websocket"
)

const (
	wsEndpoint      = "wss://openws.work.weixin.qq.com"
	wsPingInterval  = 30 * time.Second
	wsMaxBackoff    = 30 * time.Second
	wsMaxMissedPong = 2
	wecomLogBodyMax = 4000
)

// WSPlatform implements core.Platform using the WeChat Work WebSocket long-connection
// mode (智能机器人长连接). No public URL, no message encryption, no IP allowlist required.
type WSPlatform struct {
	botID       string
	secret      string
	allowFrom   string
	accessLog   *wecomAccessLogger
	conn        *websocket.Conn
	writeJSONFn func(any) error
	handler     core.MessageHandler
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex // protects conn writes
	streamMu    sync.Mutex // protects streamStates
	streamState map[string]*wsStreamState
	dedup       core.MessageDedup
	reqSeq      atomic.Int64 // monotonic counter for generating unique req_id
	missedPong  atomic.Int32 // consecutive heartbeat acks not received
	pendingAcks sync.Map     // req_id -> chan error, for sequential send with ack waiting
}

type wsStreamState struct {
	mu         sync.Mutex
	running    bool
	pending    *wsStreamSend
	lastAcked  string
	aggregator wsContentAggregator
	heldTool   string
	completed  bool
}

type wsStreamSend struct {
	content string
	finish  bool
	done    chan error
}

type wsContentAggregator struct {
	plainSegments  []string
	pendingTool    string
	hasPendingTool bool
}

const wsAckTimeout = 5 * time.Second

const wecomToolBlockPrefix = "🔧 **"

func (a *wsContentAggregator) reset() {
	a.plainSegments = nil
	a.pendingTool = ""
	a.hasPendingTool = false
}

func (a *wsContentAggregator) ingest(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return a.render()
	}
	if !a.hasPendingTool {
		rendered := a.render()
		switch {
		case rendered == trimmed:
			return rendered
		case rendered != "" && strings.HasPrefix(trimmed, rendered):
			a.plainSegments = []string{trimmed}
			return trimmed
		}
	}
	if a.shouldHoldOnlyTool(trimmed) {
		a.pendingTool = trimmed
		a.hasPendingTool = true
		return a.render()
	}
	if a.hasPendingTool {
		a.plainSegments = append(a.plainSegments, a.pendingTool)
		a.pendingTool = ""
		a.hasPendingTool = false
	}
	a.plainSegments = append(a.plainSegments, trimmed)
	return a.render()
}

func (a *wsContentAggregator) finalize(content string) string {
	if strings.TrimSpace(content) != "" {
		_ = a.ingest(content)
	}
	if a.hasPendingTool {
		a.plainSegments = append(a.plainSegments, a.pendingTool)
		a.pendingTool = ""
		a.hasPendingTool = false
	}
	out := a.render()
	a.reset()
	return out
}

func (a *wsContentAggregator) render() string {
	parts := make([]string, 0, len(a.plainSegments))
	for _, seg := range a.plainSegments {
		seg = strings.TrimSpace(seg)
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (a *wsContentAggregator) shouldHoldOnlyTool(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, wecomToolBlockPrefix) {
		return false
	}
	if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
		if suffix := strings.TrimSpace(trimmed[idx+3:]); suffix != "" {
			return false
		}
	}
	return true
}

func shouldAggregateWecomStream(content string) bool {
	return strings.Contains(content, "\n\n") || strings.HasPrefix(strings.TrimSpace(content), wecomToolBlockPrefix)
}

// wsReplyContext holds the context needed to reply to a specific message.
type wsReplyContext struct {
	reqID    string // req_id from headers of aibot_msg_callback
	chatID   string // chatid for aibot_send_msg
	chatType string // chattype: "single" or "group"
	userID   string // from.userid
	streamID string // stream id for aibot_respond_msg full-replacement updates
}

type wsPreviewHandle struct {
	replyCtx wsReplyContext
}

const wecomStreamMaxBytes = 2048

// --- WebSocket protocol frame types (matching official SDK) ---

// wsFrame is the unified frame structure used for all WebSocket communication.
// Format: { cmd, headers: { req_id }, body: {...} }
// Response frames may omit cmd and include errcode/errmsg instead.
type wsFrame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers wsFrameHeaders  `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode *int            `json:"errcode,omitempty"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

type wsFrameHeaders struct {
	ReqID string `json:"req_id"`
}

// wsMsgCallbackBody is the body of an aibot_msg_callback frame.
type wsMsgCallbackBody struct {
	MsgID    string `json:"msgid"`
	AibotID  string `json:"aibotid"`
	ChatID   string `json:"chatid"`
	ChatType string `json:"chattype"` // "single" or "group"
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
	// Voice: official field is content; some payloads used text — accept both.
	Voice struct {
		Text    string `json:"text,omitempty"`
		Content string `json:"content,omitempty"`
	} `json:"voice"`
	Image *struct {
		URL    string `json:"url"`
		Aeskey string `json:"aeskey"`
	} `json:"image,omitempty"`
	File *struct {
		URL    string `json:"url"`
		Aeskey string `json:"aeskey"`
	} `json:"file,omitempty"`
	Mixed      *wsMixedBlock `json:"mixed,omitempty"`
	Quote      *wsQuoteBlock `json:"quote,omitempty"`
	CreateTime int64         `json:"create_time"`
}

func wsVoiceText(v struct {
	Text    string `json:"text,omitempty"`
	Content string `json:"content,omitempty"`
}) string {
	if s := strings.TrimSpace(v.Content); s != "" {
		return s
	}
	return strings.TrimSpace(v.Text)
}

func newWebSocket(opts map[string]any) (core.Platform, error) {
	botID, _ := opts["bot_id"].(string)
	secret, _ := opts["bot_secret"].(string)
	if botID == "" || secret == "" {
		return nil, fmt.Errorf("wecom-ws: bot_id and bot_secret are required for websocket mode")
	}
	allowFrom, _ := opts["allow_from"].(string)
	dataDir, _ := opts["cc_data_dir"].(string)
	project, _ := opts["cc_project"].(string)

	return &WSPlatform{
		botID:       botID,
		secret:      secret,
		allowFrom:   allowFrom,
		accessLog:   newWecomAccessLogger(dataDir, project),
		streamState: make(map[string]*wsStreamState),
	}, nil
}

// generateReqID creates a unique req_id with the given prefix (e.g. "ping_1", "aibot_subscribe_2").
func (p *WSPlatform) generateReqID(prefix string) string {
	seq := p.reqSeq.Add(1)
	return fmt.Sprintf("%s_%d", prefix, seq)
}

func (p *WSPlatform) Name() string { return "wecom" }

func (p *WSPlatform) StreamPreviewMode() string { return "tool_hold" }

func (p *WSPlatform) Start(handler core.MessageHandler) error {
	p.handler = handler
	p.ctx, p.cancel = context.WithCancel(context.Background())

	go p.connectLoop()
	return nil
}

// connectLoop establishes the WebSocket connection and reconnects on failure with
// exponential backoff (1s → 2s → 4s → ... → 30s max).
func (p *WSPlatform) connectLoop() {
	backoff := time.Second
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		start := time.Now()
		err := p.runConnection()
		if p.ctx.Err() != nil {
			return // shutting down
		}

		// If the connection was alive for a meaningful period, reset backoff
		if time.Since(start) > 2*wsPingInterval {
			backoff = time.Second
		}

		slog.Warn("wecom-ws: connection lost, reconnecting", "error", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-p.ctx.Done():
			return
		}

		backoff *= 2
		if backoff > wsMaxBackoff {
			backoff = wsMaxBackoff
		}
	}
}

// runConnection dials, subscribes, and processes messages until disconnection.
func (p *WSPlatform) runConnection() error {
	slog.Info("wecom-ws: connecting", "endpoint", wsEndpoint)

	conn, _, err := websocket.DefaultDialer.DialContext(p.ctx, wsEndpoint, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.conn = nil
		p.mu.Unlock()
		conn.Close()

		// Drain pending ACK channels so waiting goroutines are unblocked
		// and stale entries do not accumulate across reconnections.
		// Collect keys first, then delete — Range+Delete in callback is
		// not guaranteed safe by the sync.Map contract.
		var staleKeys []any
		p.pendingAcks.Range(func(key, value any) bool {
			if ch, ok := value.(chan error); ok {
				select {
				case ch <- fmt.Errorf("wecom-ws: connection closed"):
				default:
				}
			}
			staleKeys = append(staleKeys, key)
			return true
		})
		for _, k := range staleKeys {
			p.pendingAcks.Delete(k)
		}
	}()

	// Send subscribe (auth) frame
	// Format: { cmd: "aibot_subscribe", headers: { req_id }, body: { bot_id, secret } }
	subReqID := p.generateReqID("aibot_subscribe")
	subFrame := map[string]any{
		"cmd":     "aibot_subscribe",
		"headers": map[string]string{"req_id": subReqID},
		"body": map[string]string{
			"bot_id": p.botID,
			"secret": p.secret,
		},
	}
	if err := p.writeJSON(subFrame); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	// Read subscribe response: { headers: { req_id }, errcode: 0, errmsg: "ok" }
	var subResp wsFrame
	if err := conn.ReadJSON(&subResp); err != nil {
		return fmt.Errorf("subscribe response: %w", err)
	}
	if subResp.ErrCode == nil || *subResp.ErrCode != 0 {
		errCode := 0
		if subResp.ErrCode != nil {
			errCode = *subResp.ErrCode
		}
		return fmt.Errorf("subscribe failed: errcode=%d errmsg=%s", errCode, subResp.ErrMsg)
	}
	slog.Info("wecom-ws: subscribed successfully", "bot_id", p.botID)
	p.missedPong.Store(0)

	// Start heartbeat goroutine
	heartCtx, heartCancel := context.WithCancel(p.ctx)
	defer heartCancel()
	go p.heartbeat(heartCtx, conn)

	// Read loop
	for {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var frame wsFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			slog.Warn("wecom-ws: invalid json", "error", err)
			continue
		}

		p.handleFrame(frame)
	}
}

// handleFrame dispatches incoming frames by cmd or req_id prefix.
func (p *WSPlatform) handleFrame(frame wsFrame) {
	switch frame.Cmd {
	case "aibot_msg_callback":
		p.handleMsgCallback(frame)
	case "aibot_event_callback":
		slog.Debug("wecom-ws: event callback received (ignored)", "req_id", frame.Headers.ReqID)
	case "":
		// Response frame (no cmd): identify by req_id prefix
		reqID := frame.Headers.ReqID
		switch {
		case strings.HasPrefix(reqID, "ping"):
			p.missedPong.Store(0)
			slog.Debug("wecom-ws: heartbeat ack received")
		case strings.HasPrefix(reqID, "aibot_subscribe"):
			// Late subscribe ack (should have been consumed in runConnection)
			slog.Debug("wecom-ws: late subscribe ack")
		default:
			var ackErr error
			if frame.ErrCode != nil && *frame.ErrCode != 0 {
				ackErr = fmt.Errorf("wecom-ws: ack error: errcode=%d errmsg=%s", *frame.ErrCode, frame.ErrMsg)
				slog.Warn("wecom-ws: reply/send ack error", "req_id", reqID, "errcode", *frame.ErrCode, "errmsg", frame.ErrMsg)
			} else {
				slog.Debug("wecom-ws: reply/send ack ok", "req_id", reqID)
			}
			if ch, ok := p.pendingAcks.LoadAndDelete(reqID); ok {
				ch.(chan error) <- ackErr
			}
		}
	default:
		slog.Debug("wecom-ws: unhandled cmd", "cmd", frame.Cmd)
	}
}

func (p *WSPlatform) heartbeat(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			missed := int(p.missedPong.Load())
			if missed >= wsMaxMissedPong {
				slog.Warn("wecom-ws: no heartbeat ack for consecutive pings, connection considered dead",
					"missed", missed)
				conn.Close()
				return
			}

			p.missedPong.Add(1)
			pingFrame := map[string]any{
				"cmd":     "ping",
				"headers": map[string]string{"req_id": p.generateReqID("ping")},
			}
			if err := p.writeJSON(pingFrame); err != nil {
				slog.Warn("wecom-ws: ping failed", "error", err)
				return
			}
			slog.Debug("wecom-ws: ping sent", "missed_pong", p.missedPong.Load())
		}
	}
}

func (p *WSPlatform) handleMsgCallback(frame wsFrame) {
	var body wsMsgCallbackBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		slog.Warn("wecom-ws: parse msg_callback body failed", "error", err)
		return
	}

	reqID := frame.Headers.ReqID

	if p.dedup.IsDuplicate(body.MsgID) {
		slog.Debug("wecom-ws: skipping duplicate message", "msg_id", body.MsgID)
		return
	}

	if body.CreateTime > 0 {
		if core.IsOldMessage(time.Unix(body.CreateTime, 0)) {
			slog.Debug("wecom-ws: ignoring old message", "create_time", body.CreateTime)
			return
		}
	}

	if !core.AllowList(p.allowFrom, body.From.UserID) {
		chatID := body.ChatID
		if chatID == "" {
			chatID = body.From.UserID
		}
		rctx := wsReplyContext{
			chatID:   chatID,
			chatType: body.ChatType,
			userID:   body.From.UserID,
		}
		p.logAccess(wecomAccessRecord{
			Source:     "websocket",
			Allowed:    false,
			UserID:     body.From.UserID,
			ChatID:     chatID,
			ChatType:   body.ChatType,
			SessionKey: fmt.Sprintf("wecom:%s:%s", chatID, body.From.UserID),
			MessageID:  body.MsgID,
			MsgType:    body.MsgType,
			Reason:     "allow_from_rejected",
		})
		denyMsg := fmt.Sprintf("无权限使用此机器人，请联系管理员开通。你的 UserID: %s", body.From.UserID)
		if err := p.Send(context.Background(), rctx, denyMsg); err != nil {
			slog.Warn("wecom-ws: failed to notify unauthorized user", "user", body.From.UserID, "chat_id", chatID, "error", err)
		}
		slog.Debug("wecom-ws: message from unauthorized user", "user", body.From.UserID)
		return
	}

	chatID := body.ChatID
	if chatID == "" {
		chatID = body.From.UserID
	}

	sessionKey := fmt.Sprintf("wecom:%s:%s", chatID, body.From.UserID)
	rctx := wsReplyContext{
		reqID:    reqID,
		chatID:   chatID,
		chatType: body.ChatType,
		userID:   body.From.UserID,
	}
	p.logAccess(wecomAccessRecord{
		Source:     "websocket",
		Allowed:    true,
		UserID:     body.From.UserID,
		ChatID:     chatID,
		ChatType:   body.ChatType,
		SessionKey: sessionKey,
		MessageID:  body.MsgID,
		MsgType:    body.MsgType,
		Reason:     "received",
	})

	// WS mode does not provide display names; the protocol only carries userID.
	// Name resolution would require a separate HTTP API call with corpSecret,
	// which is unavailable in WebSocket-only mode.
	chatName := ""
	if body.ChatType == "group" {
		chatName = body.ChatID
	}

	texts, imgRefs, fileRefs := wsCollectInboundParts(&body)

	switch body.MsgType {
	case "voice":
		vt := stripWeComAtMentions(wsVoiceText(body.Voice), p.botID, body.AibotID)
		if vt == "" && len(imgRefs) == 0 && len(fileRefs) == 0 {
			slog.Debug("wecom-ws: voice message with empty transcription, ignoring")
			return
		}
		if len(imgRefs) > 0 || len(fileRefs) > 0 {
			out := []string{}
			if vt != "" {
				out = append(out, vt)
			}
			out = append(out, texts...)
			slog.Info("wecom-ws: voice + media", "user", body.From.UserID, "images", len(imgRefs), "files", len(fileRefs))
			go p.deliverWSMediaInbound(&body, sessionKey, chatName, rctx, out, imgRefs, fileRefs)
			return
		}
		slog.Debug("wecom-ws: voice received (transcribed)", "user", body.From.UserID, "len", len(vt))
		go p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "wecom",
			MessageID: body.MsgID,
			UserID:    body.From.UserID, UserName: body.From.UserID,
			ChatName: chatName,
			Content:  vt, ReplyCtx: rctx, FromVoice: true,
		})
		return
	}

	if len(imgRefs) == 0 && len(fileRefs) == 0 {
		if len(texts) == 0 {
			slog.Warn("wecom-ws: no text or media in message", "msg_type", body.MsgType, "msg_id", body.MsgID)
			return
		}
		content := stripWeComAtMentions(strings.Join(texts, "\n"), p.botID, body.AibotID)
		slog.Debug("wecom-ws: text received", "user", body.From.UserID, "len", len(content))
		go p.handler(p, &core.Message{
			SessionKey: sessionKey, Platform: "wecom",
			MessageID: body.MsgID,
			UserID:    body.From.UserID, UserName: body.From.UserID,
			ChatName: chatName,
			Content:  content, ReplyCtx: rctx,
		})
		return
	}

	slog.Info("wecom-ws: media message", "msg_type", body.MsgType, "user", body.From.UserID,
		"images", len(imgRefs), "files", len(fileRefs), "text_parts", len(texts))
	go p.deliverWSMediaInbound(&body, sessionKey, chatName, rctx, texts, imgRefs, fileRefs)
}

func (p *WSPlatform) logAccess(rec wecomAccessRecord) {
	if p == nil || p.accessLog == nil {
		return
	}
	p.accessLog.Log(rec)
}

// Reply sends a response message via aibot_respond_msg using the stream format.
// Uses the req_id from the original callback.
// The stream content field is a full-replacement (not incremental append), so we
// send the complete content in one frame with finish=true.
// Markdown is natively supported by the stream reply format.
func (p *WSPlatform) Reply(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(wsReplyContext)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid reply context type %T", rctx)
	}
	if content == "" {
		return nil
	}

	if err := p.sendFinalReplyChunks(ctx, rc, content); err != nil {
		slog.Error("wecom-ws: reply failed", "user", rc.userID, "error", err)
		return err
	}
	slog.Debug("wecom-ws: reply sent", "user", rc.userID, "len", len(content))
	return nil
}

func (p *WSPlatform) SendPreviewStart(ctx context.Context, rctx any, content string) (any, error) {
	rc, ok := rctx.(wsReplyContext)
	if !ok {
		return nil, fmt.Errorf("wecom-ws: invalid reply context type %T", rctx)
	}
	if rc.reqID == "" {
		return nil, core.ErrNotSupported
	}
	handle := &wsPreviewHandle{replyCtx: rc}
	previewContent := wecomPreviewPayload(content)
	handle.replyCtx.streamID = p.generateReqID("stream")
	if err := p.sendStreamFrameAndWaitAck(ctx, handle.replyCtx, previewContent, false); err != nil {
		return nil, err
	}
	return handle, nil
}

func (p *WSPlatform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid preview handle type %T", previewHandle)
	}
	if h.replyCtx.streamID == "" {
		return fmt.Errorf("wecom-ws: preview handle missing stream id")
	}
	return p.sendStreamFrameAndWaitAck(ctx, h.replyCtx, wecomPreviewPayload(content), false)
}

func (p *WSPlatform) FinalizePreviewMessage(ctx context.Context, previewHandle any, content string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid preview handle type %T", previewHandle)
	}
	if h.replyCtx.streamID == "" {
		return fmt.Errorf("wecom-ws: preview handle missing stream id")
	}
	if content == "" {
		return nil
	}
	return p.sendFinalReplyChunks(ctx, h.replyCtx, content)
}

func (p *WSPlatform) sendFinalReplyChunks(ctx context.Context, rc wsReplyContext, content string) error {
	chunks := splitByBytes(content, wecomStreamMaxBytes)
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		return p.sendStreamFrameAndWaitAck(ctx, rc, chunks[0], true)
	}
	finalRC := rc
	if finalRC.streamID == "" {
		finalRC.streamID = p.generateReqID("stream")
	}
	if err := p.sendStreamFrameAndWaitAck(ctx, finalRC, chunks[0], true); err != nil {
		return err
	}
	for i := 1; i < len(chunks); i++ {
		if err := p.Send(ctx, rc, chunks[i]); err != nil {
			return err
		}
	}
	return nil
}

func wecomPreviewPayload(content string) string {
	chunks := splitByBytes(content, wecomStreamMaxBytes)
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	notice := "\n\n[内容较长，正在整理后续片段...]"
	headMax := wecomStreamMaxBytes - len([]byte(notice))
	if headMax <= 0 {
		return splitByBytes(notice, wecomStreamMaxBytes)[0]
	}
	head := splitByBytes(chunks[0], headMax)
	if len(head) == 0 {
		return splitByBytes(notice, wecomStreamMaxBytes)[0]
	}
	return head[0] + notice
}

func (p *WSPlatform) sendStreamFrameAndWaitAck(ctx context.Context, rc wsReplyContext, content string, finish bool) error {
	slog.Info("wecom-ws: stream enqueue",
		"user", rc.userID,
		"stream_id", rc.streamID,
		"finish", finish,
		"content", truncateWecomLogBody(content),
	)
	key, state, err := p.streamStateFor(rc)
	if err != nil {
		return err
	}
	return p.enqueueLatestStreamSend(ctx, key, state, rc, content, finish)
}

func (p *WSPlatform) streamStateFor(rc wsReplyContext) (string, *wsStreamState, error) {
	if rc.reqID == "" {
		return "", nil, fmt.Errorf("wecom-ws: reqID is empty, cannot send stream reply")
	}
	streamID := rc.streamID
	if streamID == "" {
		streamID = "reply"
	}
	key := rc.reqID + ":" + streamID

	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.streamState == nil {
		p.streamState = make(map[string]*wsStreamState)
	}
	state := p.streamState[key]
	if state == nil {
		state = &wsStreamState{}
		p.streamState[key] = state
	}
	return key, state, nil
}

func (p *WSPlatform) enqueueLatestStreamSend(ctx context.Context, key string, state *wsStreamState, rc wsReplyContext, content string, finish bool) error {
	req := &wsStreamSend{content: content, finish: finish, done: make(chan error, 1)}
	var superseded *wsStreamSend

	state.mu.Lock()
	if !finish && state.lastAcked == content {
		state.mu.Unlock()
		return nil
	}
	if !finish && shouldAggregateWecomStream(content) && state.aggregator.shouldHoldOnlyTool(content) {
		state.heldTool = strings.TrimSpace(content)
		state.completed = false
		state.mu.Unlock()
		slog.Info("wecom-ws: stream hold tool-only", "key", key, "content", truncateWecomLogBody(content))
		return nil
	}
	if pending := state.pending; pending != nil {
		if finish || !pending.finish {
			superseded = pending
			state.pending = req
		} else {
			// A terminal finish frame is already queued; later non-finish updates are obsolete.
			state.mu.Unlock()
			return nil
		}
		state.mu.Unlock()
		if superseded != nil {
			superseded.done <- nil
			close(superseded.done)
		}
		select {
		case err := <-req.done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.pending = req
	state.completed = false
	if state.running {
		state.mu.Unlock()
		select {
		case err := <-req.done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.running = true
	state.mu.Unlock()

	go p.runStreamQueue(key, state, rc)

	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *WSPlatform) runStreamQueue(key string, state *wsStreamState, rc wsReplyContext) {
	for {
		state.mu.Lock()
		req := state.pending
		state.pending = nil
		state.mu.Unlock()

		if req == nil {
			state.mu.Lock()
			state.running = false
			idle := state.pending == nil
			completed := state.completed
			state.mu.Unlock()
			if idle {
				if completed {
					p.streamMu.Lock()
					delete(p.streamState, key)
					p.streamMu.Unlock()
				}
				return
			}
			state.mu.Lock()
			if !state.running {
				state.running = true
			}
			state.mu.Unlock()
			continue
		}

		state.mu.Lock()
		aggregateThis := shouldAggregateWecomStream(req.content) || state.heldTool != "" || state.aggregator.hasPendingTool || len(state.aggregator.plainSegments) > 0
		trimmedReq := strings.TrimSpace(req.content)
		trimmedLastAcked := strings.TrimSpace(state.lastAcked)
		if aggregateThis && len(state.aggregator.plainSegments) == 0 && !state.aggregator.hasPendingTool && trimmedLastAcked != "" && !strings.HasPrefix(trimmedReq, trimmedLastAcked) && !strings.HasPrefix(trimmedReq, wecomToolBlockPrefix) {
			state.aggregator.plainSegments = append(state.aggregator.plainSegments, trimmedLastAcked)
		}
		if aggregateThis && state.heldTool != "" {
			state.aggregator.pendingTool = state.heldTool
			state.aggregator.hasPendingTool = true
			state.heldTool = ""
		}
		rendered := req.content
		if !aggregateThis {
			if req.finish {
				state.heldTool = ""
			}
			lastAckedMatches := !req.finish && state.lastAcked == rendered
			state.mu.Unlock()

			if lastAckedMatches {
				slog.Info("wecom-ws: stream skip duplicate", "key", key, "finish", req.finish, "content", truncateWecomLogBody(rendered))
				req.done <- nil
				close(req.done)
				continue
			}

			reqID, frame, err := p.buildStreamFrame(rc, rendered, req.finish)
			if err == nil {
				err = p.writeAndWaitAck(context.Background(), frame, reqID)
			}
			if err == nil && !req.finish {
				state.mu.Lock()
				state.lastAcked = rendered
				state.mu.Unlock()
			}
			if req.finish && err == nil {
				state.mu.Lock()
				state.lastAcked = rendered
				state.completed = true
				state.mu.Unlock()
			}
			req.done <- err
			close(req.done)
			continue
		}

		if req.finish {
			rendered = state.aggregator.finalize(req.content)
		} else {
			rendered = state.aggregator.ingest(req.content)
		}
		slog.Info("wecom-ws: stream aggregate", "key", key, "finish", req.finish, "content", truncateWecomLogBody(rendered))
		lastAckedMatches := !req.finish && state.lastAcked == rendered
		state.mu.Unlock()

		if lastAckedMatches {
			slog.Info("wecom-ws: stream skip duplicate", "key", key, "finish", req.finish, "content", truncateWecomLogBody(rendered))
			req.done <- nil
			close(req.done)
			continue
		}

		reqID, frame, err := p.buildStreamFrame(rc, rendered, req.finish)
		if err == nil {
			err = p.writeAndWaitAck(context.Background(), frame, reqID)
		}
		if err == nil && !req.finish {
			state.mu.Lock()
			state.lastAcked = rendered
			state.mu.Unlock()
		}
		if req.finish && err == nil {
			state.mu.Lock()
			state.lastAcked = rendered
			state.completed = true
			state.mu.Unlock()
		}
		req.done <- err
		close(req.done)
	}
}

func (p *WSPlatform) buildStreamFrame(rc wsReplyContext, content string, finish bool) (string, map[string]any, error) {
	if rc.reqID == "" {
		return "", nil, fmt.Errorf("wecom-ws: reqID is empty, cannot send stream reply")
	}
	streamID := rc.streamID
	if streamID == "" {
		streamID = p.generateReqID("stream")
	}
	frame := map[string]any{
		"cmd":     "aibot_respond_msg",
		"headers": map[string]string{"req_id": rc.reqID},
		"body": map[string]any{
			"msgtype": "stream",
			"stream": map[string]any{
				"id":      streamID,
				"finish":  finish,
				"content": content,
			},
		},
	}
	slog.Info("wecom-ws: stream frame prepared", "user", rc.userID, "stream_id", streamID, "finish", finish, "content", truncateWecomLogBody(content))
	return rc.reqID, frame, nil
}

func truncateWecomLogBody(content string) string {
	content = strings.TrimSpace(content)
	if len(content) <= wecomLogBodyMax {
		return content
	}
	return content[:wecomLogBodyMax] + "...<truncated>"
}

// Send sends a proactive message via aibot_send_msg (markdown format).
// Used for follow-up messages and cron-triggered messages where no req_id is available.
// Markdown is natively supported.
func (p *WSPlatform) Send(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(wsReplyContext)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid reply context type %T", rctx)
	}
	if content == "" {
		return nil
	}
	if rc.chatID == "" {
		return fmt.Errorf("wecom-ws: chatID is empty, cannot send proactive message")
	}

	chunks := splitByBytes(content, wecomStreamMaxBytes)
	for i, chunk := range chunks {
		reqID := p.generateReqID("aibot_send_msg")
		frame := map[string]any{
			"cmd":     "aibot_send_msg",
			"headers": map[string]string{"req_id": reqID},
			"body": map[string]any{
				"chatid":  rc.chatID,
				"msgtype": "markdown",
				"markdown": map[string]string{
					"content": chunk,
				},
			},
		}
		if err := p.writeAndWaitAck(ctx, frame, reqID); err != nil {
			slog.Error("wecom-ws: send failed", "user", rc.userID, "chunk", i, "error", err)
			return err
		}
	}
	slog.Debug("wecom-ws: message sent", "user", rc.userID, "chunks", len(chunks), "total_len", len(content))
	return nil
}

// ReconstructReplyCtx rebuilds a reply context from a session key.
// Session key format: "wecom:{chatID}:{userID}".
// The reconstructed context has no req_id, so Reply() (which needs req_id for
// aibot_respond_msg) won't work — the engine should use Send() (aibot_send_msg)
// for cron/relay scenarios.
func (p *WSPlatform) ReconstructReplyCtx(sessionKey string) (any, error) {
	// wecom:{chatID}:{userID}
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) < 3 || parts[0] != "wecom" {
		return nil, fmt.Errorf("wecom-ws: invalid session key %q", sessionKey)
	}
	return wsReplyContext{chatID: parts[1], userID: parts[2]}, nil
}

func (p *WSPlatform) Stop() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// writeJSON sends a JSON message over the WebSocket connection with mutex protection.
func (p *WSPlatform) writeJSON(v any) error {
	if p.writeJSONFn != nil {
		return p.writeJSONFn(v)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return fmt.Errorf("wecom-ws: not connected")
	}
	return p.conn.WriteJSON(v)
}

// writeAndWaitAck sends a frame and waits for the server ack before returning.
// Falls back to non-blocking on timeout to avoid deadlocks.
func (p *WSPlatform) writeAndWaitAck(ctx context.Context, frame map[string]any, reqID string) error {
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	if err := p.writeJSON(frame); err != nil {
		p.pendingAcks.Delete(reqID)
		return err
	}

	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		p.pendingAcks.Delete(reqID)
		return ctx.Err()
	case <-time.After(wsAckTimeout):
		p.pendingAcks.Delete(reqID)
		slog.Debug("wecom-ws: ack timeout, proceeding", "req_id", reqID)
		return nil
	}
}
