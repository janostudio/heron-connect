package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// controllableAgent emits a scripted event sequence when Send is called, so we
// can drive a full interactive turn (thinking -> tool -> answer) through the
// engine and observe the exact bridge messages the Web frontend receives.
type thinkingAgent struct {
	mu      sync.Mutex
	session *thinkingSession
}

type thinkingSession struct {
	events   chan core.Event
	sentOnce sync.Once
	sent     []string
}

func newThinkingSession() *thinkingSession {
	return &thinkingSession{events: make(chan core.Event, 32)}
}

func (a *thinkingAgent) Name() string { return "thinking" }
func (a *thinkingAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		a.session = newThinkingSession()
	}
	return a.session, nil
}
func (a *thinkingAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) { return nil, nil }
func (a *thinkingAgent) Stop() error                                                     { return nil }

func (s *thinkingSession) Send(prompt string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	s.sent = append(s.sent, prompt)
	s.sentOnce.Do(func() {
		go func() {
			s.events <- core.Event{Type: core.EventThinking, Content: "Let me inspect the repo first."}
			s.events <- core.Event{Type: core.EventToolUse, ToolName: "Bash", ToolInput: "git status"}
			s.events <- core.Event{Type: core.EventToolResult, ToolName: "Bash", Content: "On branch main\nnothing to commit"}
			s.events <- core.Event{Type: core.EventText, Content: "The working tree is clean."}
			s.events <- core.Event{Type: core.EventResult, Content: "The working tree is clean.", Done: true}
			close(s.events)
		}()
	})
	return nil
}
func (s *thinkingSession) RespondPermission(_ string, _ core.PermissionResult) error { return nil }
func (s *thinkingSession) Events() <-chan core.Event                                 { return s.events }
func (s *thinkingSession) CurrentSessionID() string                                  { return "think-session" }
func (s *thinkingSession) Alive() bool                                               { return true }
func (s *thinkingSession) Close() error                                              { return nil }
func (s *thinkingSession) CancelTurn()                                               {}

// TestBridge_WebThinkingToolPersist verifies that for the Web platform
// (capabilities exactly as the frontend registers them) thinking/tool progress
// previews are sent and NOT deleted, and the final answer does not clobber them.
func TestBridge_WebThinkingToolPersist(t *testing.T) {
	bs, wsURL := startTestBridge(t, "")

	bp := bs.NewPlatform("test-proj")
	agent := &thinkingAgent{}
	e := core.NewEngine("test-proj", agent, []core.Platform{bp}, "", core.LangEnglish)
	// full display => thinking + tool messages enabled (mirrors user config).
	e.SetDisplayConfig(core.DisplayCfg{Mode: "full", ThinkingMessages: true, ToolMessages: true})
	bs.RegisterEngine("test-proj", e, bp)

	conn := dialWS(t, wsURL, nil)
	// Capabilities exactly as web/src/hooks/useBridgeSocket.ts registers them.
	register(t, conn, "web", []string{"text", "card", "buttons", "typing", "update_message", "preview", "reconstruct_reply"})

	// Reader goroutine: collects outbound messages and acks previews (exactly
	// like the Web frontend) so the engine turn is not blocked on preview_ack.
	got := map[string][]map[string]any{}
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		ackCounter := 0
		for {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			typ, _ := msg["type"].(string)
			if typ == "preview_start" {
				ackCounter++
				handle := "web-preview-" + itoa(ackCounter)
				mustWriteJSON(t, conn, map[string]any{
					"type":          "preview_ack",
					"ref_id":        msg["ref_id"],
					"preview_handle": handle,
				})
			}
			mu.Lock()
			got[typ] = append(got[typ], msg)
			mu.Unlock()
			if typ == "reply" || (typ == "reply_stream" && msg["done"] == true) {
				// Give the engine a moment to flush any trailing updates, then stop.
				time.Sleep(300 * time.Millisecond)
				return
			}
		}
	}()

	mustWriteJSON(t, conn, map[string]any{
		"type":        "message",
		"msg_id":      "m1",
		"session_key": "web:web-admin:test-proj",
		"user_id":     "web-admin",
		"content":     "check git status",
		"reply_ctx":   "web:web-admin:test-proj",
		"project":     "test-proj",
	})

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for turn to complete")
	}

	mu.Lock()
	defer mu.Unlock()

	previews := got["preview_start"]
	updates := got["update_message"]
	deletes := got["delete_message"]
	replies := got["reply"]
	replyStreams := got["reply_stream"]

	if len(previews) == 0 {
		t.Fatalf("expected at least one preview_start (thinking/tool), got none. all types: %v", keysOf(got))
	}

	if len(deletes) != 0 {
		t.Errorf("unexpected delete_message sent to web (capability absent): %#v", deletes)
	}

	// The thinking/tool content must actually be delivered to the frontend as a
	// preview (this is what was "never seen" before the fix).
	thinkingDelivered := false
	for _, p := range previews {
		if c, ok := p["content"].(string); ok && containsThinking(c) {
			thinkingDelivered = true
		}
	}
	for _, u := range updates {
		if c, ok := u["content"].(string); ok && containsThinking(c) {
			thinkingDelivered = true
		}
	}
	if !thinkingDelivered {
		t.Errorf("thinking/tool content was never delivered as a preview; previews=%#v updates=%#v", previews, updates)
	}

	if len(replies) == 0 && len(replyStreams) == 0 {
		t.Fatalf("expected a final answer reply, got none. previews=%d updates=%d", len(previews), len(updates))
	}

	// The backend delivers the answer as a SEPARATE `reply`, not by updating the
	// thinking/tool preview in place. A correct frontend must therefore append the
	// answer as a new message and keep the thinking/tool preview, rather than
	// replacing the first streaming assistant message (the old behaviour that
	// made thinking/tool disappear).
	t.Logf("previews=%d updates=%d deletes=%d replies=%d reply_streams=%d thinkingDelivered=%v",
		len(previews), len(updates), len(deletes), len(replies), len(replyStreams), thinkingDelivered)
}

func containsThinking(s string) bool {
	return len(s) > 0 && (indexOf(s, "inspect") >= 0 || indexOf(s, "Thinking") >= 0 || indexOf(s, "💭") >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func keysOf(m map[string][]map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
