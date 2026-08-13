package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// mockUpdaterPlatform implements Platform + MessageUpdater + PreviewStarter.
type mockUpdaterPlatform struct {
	stubPlatformEngine
	mu       sync.Mutex
	messages []string // track all sent/updated messages
	lastMsg  string
}

func (m *mockUpdaterPlatform) SendPreviewStart(_ context.Context, _ any, content string) (any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, "start:"+content)
	m.lastMsg = content
	return "preview-handle", nil
}

func (m *mockUpdaterPlatform) UpdateMessage(_ context.Context, _ any, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, "update:"+content)
	m.lastMsg = content
	return nil
}

func (m *mockUpdaterPlatform) FinalizePreviewMessage(_ context.Context, _ any, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, "finalize:"+content)
	m.lastMsg = content
	return nil
}

func (m *mockUpdaterPlatform) getMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.messages))
	copy(out, m.messages)
	return out
}

type rolloverMockPlatform struct {
	mockUpdaterPlatform
	sendErrors []error
}

func (m *rolloverMockPlatform) Send(ctx context.Context, replyCtx any, content string) error {
	m.mockUpdaterPlatform.mu.Lock()
	defer m.mockUpdaterPlatform.mu.Unlock()
	if len(m.sendErrors) > 0 {
		err := m.sendErrors[0]
		m.sendErrors = m.sendErrors[1:]
		if err != nil {
			return err
		}
	}
	m.mockUpdaterPlatform.messages = append(m.mockUpdaterPlatform.messages, "send:"+content)
	return nil
}

func (m *rolloverMockPlatform) StreamPreviewRolloverConfig() StreamPreviewRolloverConfig {
	return StreamPreviewRolloverConfig{PreviewMaxBytes: 10, FollowupMaxBytes: 6}
}

func TestTakeStreamSegment_UTF8ByteBoundary(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxBytes int
		want     string
	}{
		{
			name:     "preview boundary before Chinese rune",
			text:     strings.Repeat("a", 16383) + "你tail",
			maxBytes: 16384,
			want:     strings.Repeat("a", 16383),
		},
		{
			name:     "follow-up boundary before Chinese rune",
			text:     strings.Repeat("b", 2047) + "好tail",
			maxBytes: 2048,
			want:     strings.Repeat("b", 2047),
		},
		{
			name:     "boundary before emoji rune",
			text:     strings.Repeat("c", 2046) + "🧪tail",
			maxBytes: 2048,
			want:     strings.Repeat("c", 2046),
		},
		{
			name:     "prefers paragraph boundary",
			text:     "first paragraph\n\nsecond paragraph continues",
			maxBytes: 25,
			want:     "first paragraph\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := takeStreamSegment(tt.text, tt.maxBytes, true)
			if got != tt.want {
				t.Fatalf("segment = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("segment is not valid UTF-8: %q", got)
			}
			if len([]byte(got)) > tt.maxBytes {
				t.Fatalf("segment bytes = %d, want <= %d", len([]byte(got)), tt.maxBytes)
			}
			if !strings.HasPrefix(tt.text, got) {
				t.Fatalf("segment %q is not a source prefix", got)
			}
		})
	}
}

func TestRenderFencedRolloverSegment_ClosesAndReopensCodeFence(t *testing.T) {
	sp := &streamPreview{}
	first, nextFence := sp.renderFencedRolloverSegment("```go\nfmt.Println(1)")
	if !strings.HasSuffix(first, "\n```") {
		t.Fatalf("first payload must close its code fence: %q", first)
	}
	if nextFence != "```go" {
		t.Fatalf("next fence = %q, want ```go", nextFence)
	}

	sp.rolloverFence = nextFence
	second, nextFence := sp.renderFencedRolloverSegment("fmt.Println(2)\n```\n")
	if !strings.HasPrefix(second, "```go\n") {
		t.Fatalf("second payload must reopen its code fence: %q", second)
	}
	if nextFence != "" {
		t.Fatalf("next fence = %q, want empty after closing fence", nextFence)
	}
}

func TestStreamPreview_RolloverSendsFollowupsBeforeFinish(t *testing.T) {
	mp := &rolloverMockPlatform{}
	sp := newStreamPreview(StreamPreviewCfg{Enabled: true, IntervalMs: 0, MinDeltaChars: 1, MaxChars: 4}, mp, "ctx", context.Background(), nil)

	sp.appendText("hello\nworld\nagain")
	if !sp.rolloverFinalized {
		t.Fatal("expected preview rollover after exceeding preview budget")
	}
	if !strings.Contains(strings.Join(mp.getMessages(), "\n"), "send:") {
		t.Fatal("expected a follow-up to be sent before finish")
	}
	if !sp.finish("hello\nworld\nagain") {
		t.Fatal("expected rollover preview to handle final delivery")
	}
	msgs := mp.getMessages()
	if len(msgs) < 2 || !strings.HasPrefix(msgs[0], "start:") || !strings.HasPrefix(msgs[1], "finalize:") {
		t.Fatalf("preview lifecycle = %#v, want start then finalize", msgs)
	}
	var delivered string
	for _, msg := range msgs {
		switch {
		case strings.HasPrefix(msg, "finalize:"):
			delivered = strings.TrimPrefix(msg, "finalize:")
		case strings.HasPrefix(msg, "send:"):
			delivered += strings.TrimPrefix(msg, "send:")
		}
	}
	if delivered != "hello\nworld\nagain" {
		t.Fatalf("reassembled delivery = %q", delivered)
	}
}

func TestStreamPreview_FinishRolloverRetriesOnlyUncommittedSuffix(t *testing.T) {
	mp := &rolloverMockPlatform{sendErrors: []error{errors.New("transient"), nil}}
	sp := newStreamPreview(StreamPreviewCfg{Enabled: true, IntervalMs: 0, MinDeltaChars: 1}, mp, "ctx", context.Background(), nil)
	sp.appendText("hello")

	final := "hello\nworld\nagain"
	if !sp.finish(final) {
		t.Fatal("expected retry of uncommitted follow-up to complete")
	}
	if sp.rolloverCommittedSource != final {
		t.Fatalf("committed source = %q, want %q", sp.rolloverCommittedSource, final)
	}
	for _, msg := range mp.getMessages() {
		if strings.HasPrefix(msg, "send:hello") {
			t.Fatalf("committed preview prefix was replayed as follow-up: %#v", mp.getMessages())
		}
	}
}

func TestStreamPreview_FinishRolloverFailureReturnsFalse(t *testing.T) {
	fail := errors.New("send failed")
	mp := &rolloverMockPlatform{sendErrors: []error{fail, fail}}
	sp := newStreamPreview(StreamPreviewCfg{Enabled: true, IntervalMs: 0, MinDeltaChars: 1}, mp, "ctx", context.Background(), nil)
	sp.appendText("hello")

	final := "hello\nworld\nagain"
	if sp.finish(final) {
		t.Fatal("finish should fail when the uncommitted follow-up cannot be delivered")
	}
	if sp.rolloverCommittedSource == final {
		t.Fatalf("failed follow-up must not advance committed source: %q", sp.rolloverCommittedSource)
	}
}

func TestStreamPreview_BasicFlow(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    100,
		MinDeltaChars: 5,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)

	if !sp.canPreview() {
		t.Fatal("should be able to preview")
	}

	sp.appendText("Hello ")
	time.Sleep(150 * time.Millisecond)

	msgs := mp.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least one message sent")
	}
	if msgs[0] != "start:Hello " {
		t.Errorf("first message = %q, want 'start:Hello '", msgs[0])
	}
}

func TestStreamPreview_ThrottlesUpdates(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    200,
		MinDeltaChars: 5,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)

	// Rapid-fire small appends
	for i := 0; i < 10; i++ {
		sp.appendText("ab")
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for throttle timers to fire
	time.Sleep(300 * time.Millisecond)

	msgs := mp.getMessages()
	// Should NOT have 10 individual updates; throttling should batch them
	if len(msgs) >= 10 {
		t.Errorf("expected throttling to reduce updates, got %d", len(msgs))
	}
	if len(msgs) == 0 {
		t.Error("expected at least one update")
	}
}

func TestStreamPreview_MaxChars(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      10,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("This is a very long text that exceeds max chars limit")
	time.Sleep(100 * time.Millisecond)

	msgs := mp.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	// Last message should be truncated
	for _, m := range msgs {
		if len(m) > 0 {
			// Content after "start:" or "update:" should respect maxChars
			content := m
			for _, prefix := range []string{"start:", "update:"} {
				if len(content) > len(prefix) && content[:len(prefix)] == prefix {
					content = content[len(prefix):]
				}
			}
			if len([]rune(content)) > 15 { // 10 chars + "…" with some margin
				t.Errorf("message too long: %q (%d runes)", content, len([]rune(content)))
			}
		}
	}
}

func TestStreamPreview_Disabled(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{Enabled: false}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	if sp.canPreview() {
		t.Error("should not be able to preview when disabled")
	}

	sp.appendText("Hello")
	time.Sleep(50 * time.Millisecond)

	msgs := mp.getMessages()
	if len(msgs) != 0 {
		t.Error("no messages should be sent when disabled")
	}
}

func TestStreamPreview_FinishInPlace(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)

	ok := sp.finish("Hello World Final")
	if !ok {
		t.Error("finish should return true when preview was active")
	}

	msgs := mp.getMessages()
	last := msgs[len(msgs)-1]
	if last != "finalize:Hello World Final" {
		t.Errorf("last message = %q, want 'finalize:Hello World Final'", last)
	}
}

// mockCleanerPlatform adds PreviewCleaner to mockUpdaterPlatform.
type mockCleanerPlatform struct {
	mockUpdaterPlatform
	deleted []any
}

func (m *mockCleanerPlatform) DeletePreviewMessage(_ context.Context, handle any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, handle)
	return nil
}

type mockKeepPreviewPlatform struct {
	mockCleanerPlatform
	mode string

	// ProgressAssembler call records
	muProgress    sync.Mutex
	toolStarts    []toolStartCall
	toolCompletes []toolCompleteCall
}

type toolStartCall struct {
	toolName   string
	explainArg string
	rawArg     string
}

type toolCompleteCall struct {
	toolName      string
	resultSummary string
}

func (m *mockKeepPreviewPlatform) KeepPreviewOnFinish() bool {
	return true
}

func (m *mockKeepPreviewPlatform) StreamPreviewMode() string {
	if strings.TrimSpace(m.mode) == "" {
		return ""
	}
	return m.mode
}

func (m *mockKeepPreviewPlatform) OnToolStart(_ context.Context, _ any, toolName, explainArg, rawArg string) error {
	m.muProgress.Lock()
	defer m.muProgress.Unlock()
	m.toolStarts = append(m.toolStarts, toolStartCall{toolName, explainArg, rawArg})
	return nil
}

func (m *mockKeepPreviewPlatform) OnToolComplete(_ context.Context, _ any, toolName, resultSummary string) error {
	m.muProgress.Lock()
	defer m.muProgress.Unlock()
	m.toolCompletes = append(m.toolCompletes, toolCompleteCall{toolName, resultSummary})
	return nil
}

func (m *mockKeepPreviewPlatform) getToolStarts() []toolStartCall {
	m.muProgress.Lock()
	defer m.muProgress.Unlock()
	out := make([]toolStartCall, len(m.toolStarts))
	copy(out, m.toolStarts)
	return out
}

func (m *mockKeepPreviewPlatform) getToolCompletes() []toolCompleteCall {
	m.muProgress.Lock()
	defer m.muProgress.Unlock()
	out := make([]toolCompleteCall, len(m.toolCompletes))
	copy(out, m.toolCompletes)
	return out
}

func TestStreamPreview_FreezeDeletesOnFinish(t *testing.T) {
	mp := &mockCleanerPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)

	// Simulate a tool/thinking event → freeze
	sp.freeze()

	// With degraded recovery, finish attempts UpdateMessage on the degraded
	// preview. Since mockCleanerPlatform embeds mockUpdaterPlatform,
	// UpdateMessage succeeds and finish returns true (recovered).
	ok := sp.finish("Hello World Final")
	if !ok {
		t.Error("finish should return true when degraded recovery via UpdateMessage succeeds")
	}
}

func TestStreamPreview_NonUpdaterPlatform(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	cfg := DefaultStreamPreviewCfg()

	sp := newStreamPreview(cfg, p, "ctx", context.Background(), nil)
	if sp.canPreview() {
		t.Error("should not preview on non-updater platform")
	}
}

func TestStreamPreview_DiscardDeletesPreview(t *testing.T) {
	mp := &mockCleanerPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)

	sp.discard()

	mp.mu.Lock()
	deletedCount := len(mp.deleted)
	msgs := append([]string(nil), mp.messages...)
	mp.mu.Unlock()

	if deletedCount != 1 {
		t.Fatalf("expected 1 delete call, got %d", deletedCount)
	}
	if len(msgs) != 1 || msgs[0] != "start:Hello World" {
		t.Fatalf("messages = %#v, want only initial preview", msgs)
	}
}

func TestStreamPreview_FinishKeepsPreviewWhenPlatformPrefersInPlaceFinalize(t *testing.T) {
	mp := &mockKeepPreviewPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)

	ok := sp.finish("Hello World Final")
	if !ok {
		t.Fatal("finish should return true when platform prefers in-place finalize")
	}

	mp.mu.Lock()
	deletedCount := len(mp.deleted)
	msgs := append([]string(nil), mp.messages...)
	mp.mu.Unlock()

	if deletedCount != 0 {
		t.Fatalf("expected no delete call, got %d", deletedCount)
	}
	if len(msgs) < 2 || msgs[len(msgs)-1] != "finalize:Hello World Final" {
		t.Fatalf("messages = %#v, want final update in place", msgs)
	}
}

func TestStreamPreview_NeedsDoneReaction_TrueAfterUpdate(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)

	if sp.needsDoneReaction() {
		t.Error("needsDoneReaction should be false before any send")
	}

	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)

	if sp.needsDoneReaction() {
		t.Error("needsDoneReaction should be false after only SendPreviewStart (no UpdateMessage yet)")
	}

	sp.appendText(" more text to trigger update")
	time.Sleep(100 * time.Millisecond)

	msgs := mp.getMessages()
	hasUpdate := false
	for _, m := range msgs {
		if len(m) > 7 && m[:7] == "update:" {
			hasUpdate = true
			break
		}
	}
	if !hasUpdate {
		t.Fatal("expected at least one UpdateMessage call")
	}

	if !sp.needsDoneReaction() {
		t.Error("needsDoneReaction should be true after UpdateMessage was used")
	}
}

func TestStreamPreview_NeedsDoneReaction_FalseAfterDiscard(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello World")
	time.Sleep(100 * time.Millisecond)
	sp.appendText(" more text")
	time.Sleep(100 * time.Millisecond)

	sp.discard()

	if sp.needsDoneReaction() {
		t.Error("needsDoneReaction should be false after discard (previewMsgID cleared)")
	}
}

func TestStreamPreview_NeedsDoneReaction_FalseWhenDisabled(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{Enabled: false}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("Hello")
	time.Sleep(100 * time.Millisecond)

	if sp.needsDoneReaction() {
		t.Error("needsDoneReaction should be false when preview is disabled")
	}
}

func TestStreamPreview_FinishPrefersPreviewFinalizer(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), nil)
	sp.appendText("partial")
	time.Sleep(100 * time.Millisecond)

	if !sp.finish("final answer") {
		t.Fatal("finish should succeed via PreviewFinalizer")
	}

	msgs := mp.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("messages = %v, want start + finalize", msgs)
	}
	if msgs[len(msgs)-1] != "finalize:final answer" {
		t.Fatalf("last message = %q, want finalize:final answer", msgs[len(msgs)-1])
	}
}

func TestStreamPreview_AppliesTransform(t *testing.T) {
	mp := &mockUpdaterPlatform{}
	cfg := StreamPreviewCfg{
		Enabled:       true,
		IntervalMs:    50,
		MinDeltaChars: 1,
		MaxChars:      500,
	}

	sp := newStreamPreview(cfg, mp, "ctx", context.Background(), func(s string) string {
		return strings.ReplaceAll(s, "/root/code/demo/src/app.ts:42", "📄 `src/app.ts:42`")
	})
	sp.appendText("See /root/code/demo/src/app.ts:42")
	time.Sleep(100 * time.Millisecond)

	ok := sp.finish("Final /root/code/demo/src/app.ts:42")
	if !ok {
		t.Fatal("finish should succeed when preview is active")
	}

	msgs := mp.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("messages = %#v, want preview start and final update", msgs)
	}
	if got := msgs[0]; got != "start:See 📄 `src/app.ts:42`" {
		t.Fatalf("start message = %q, want transformed preview start", got)
	}
	if got := msgs[len(msgs)-1]; got != "finalize:Final 📄 `src/app.ts:42`" {
		t.Fatalf("final message = %q, want transformed final preview", got)
	}
}
