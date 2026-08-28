package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type suppressTestPlatform struct {
	style string
}

func (s *suppressTestPlatform) Name() string                             { return "test" }
func (s *suppressTestPlatform) Start(MessageHandler) error               { return nil }
func (s *suppressTestPlatform) Reply(context.Context, any, string) error { return nil }
func (s *suppressTestPlatform) Send(context.Context, any, string) error  { return nil }
func (s *suppressTestPlatform) Stop() error                              { return nil }
func (s *suppressTestPlatform) ProgressStyle() string                    { return s.style }

func TestSuppressStandaloneToolResultEvent(t *testing.T) {
	if SuppressStandaloneToolResultEvent(&stubPlatformNoProgress{}) {
		t.Fatal("platform without ProgressStyleProvider should not suppress")
	}
	if !SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "legacy"}) {
		t.Fatal("legacy ProgressStyleProvider should suppress standalone tool results")
	}
	if SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "compact"}) {
		t.Fatal("compact should not suppress (writer absorbs tool results)")
	}
	if SuppressStandaloneToolResultEvent(&suppressTestPlatform{style: "card"}) {
		t.Fatal("card should not suppress")
	}
}

// stubPlatformNoProgress is a minimal Platform without ProgressStyleProvider.
type stubPlatformNoProgress struct{}

func (stubPlatformNoProgress) Name() string                             { return "plain" }
func (stubPlatformNoProgress) Start(MessageHandler) error               { return nil }
func (stubPlatformNoProgress) Reply(context.Context, any, string) error { return nil }
func (stubPlatformNoProgress) Send(context.Context, any, string) error  { return nil }
func (stubPlatformNoProgress) Stop() error                              { return nil }

type progressHintReplyCtx struct {
	style   string
	payload bool
}

func (r progressHintReplyCtx) ProgressStyleHint() string { return r.style }

func (r progressHintReplyCtx) SupportsProgressCardPayloadHint() bool { return r.payload }

type previewCapturePlatform struct {
	started []string
	updated []string
}

func (p *previewCapturePlatform) Name() string                             { return "bridge" }
func (p *previewCapturePlatform) ProgressStyle() string                    { return "compact" }
func (p *previewCapturePlatform) Start(MessageHandler) error               { return nil }
func (p *previewCapturePlatform) Reply(context.Context, any, string) error { return nil }
func (p *previewCapturePlatform) Send(context.Context, any, string) error  { return nil }
func (p *previewCapturePlatform) Stop() error                              { return nil }

func (p *previewCapturePlatform) SendPreviewStart(_ context.Context, _ any, content string) (any, error) {
	p.started = append(p.started, content)
	return "preview-1", nil
}

func (p *previewCapturePlatform) UpdateMessage(_ context.Context, _ any, content string) error {
	p.updated = append(p.updated, content)
	return nil
}

func (p *previewCapturePlatform) getPreviewEdits() []string {
	return p.updated
}

func TestBuildAndParseProgressCardPayload(t *testing.T) {
	payload := BuildProgressCardPayload([]string{" step1 ", "", "step2"}, true)
	if payload == "" {
		t.Fatal("BuildProgressCardPayload returned empty string")
	}
	if !strings.HasPrefix(payload, ProgressCardPayloadPrefix) {
		t.Fatalf("payload = %q, want prefix %q", payload, ProgressCardPayloadPrefix)
	}

	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("ParseProgressCardPayload should succeed, payload=%q", payload)
	}
	if len(parsed.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(parsed.Entries))
	}
	if parsed.Entries[0] != "step1" || parsed.Entries[1] != "step2" {
		t.Fatalf("entries = %#v, want [step1 step2]", parsed.Entries)
	}
	if !parsed.Truncated {
		t.Fatal("parsed.Truncated = false, want true")
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(parsed.Items))
	}
	if parsed.Items[0].Kind != ProgressEntryInfo || parsed.Items[0].Text != "step1" {
		t.Fatalf("items[0] = %#v, want info/step1", parsed.Items[0])
	}
}

// TestCompactProgressWriter_UnlimitedPlatformKeepsFullHistory verifies that a
// platform which does NOT implement MessageSizeLimitProvider (e.g. bridge/Web,
// which streams over WebSocket with no single-message size bound) never has its
// coalesced progress preview trimmed — all tool/thinking content is retained.
func TestCompactProgressWriter_UnlimitedPlatformKeepsFullHistory(t *testing.T) {
	p := &previewCapturePlatform{} // Name "bridge", no MessageSizeLimitProvider

	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)
	if !w.enabled {
		t.Fatal("progress writer should be enabled for compact-capable platform")
	}
	// maxChars must be 0 (unlimited) for a platform without the interface.
	if w.maxChars != 0 {
		t.Fatalf("maxChars = %d, want 0 (unlimited) for bridge/web", w.maxChars)
	}

	// Feed 60 long tool steps — far beyond the old 3800-char cap.
	const stepLen = 200
	for i := 0; i < 60; i++ {
		content := strings.Repeat("x", stepLen)
		if !w.AppendEvent(ProgressEntryToolUse, content, "", content) {
			t.Fatalf("AppendEvent failed at step %d", i)
		}
	}

	edits := p.getPreviewEdits()
	if len(edits) == 0 {
		t.Fatal("expected at least one preview edit")
	}
	last := edits[len(edits)-1]
	// No leading "…" truncation marker: the full history was kept.
	if strings.HasPrefix(last, "…") {
		t.Fatalf("unlimited platform preview was trimmed (starts with …): %q", last[:min(40, len(last))])
	}
	// All 60 steps should be present (60 * 200 = 12000 chars, well past old cap).
	if !strings.Contains(last, strings.Repeat("x", stepLen)) {
		t.Fatal("expected tool content to be present in unlimited preview")
	}
	if got := strings.Count(last, strings.Repeat("x", stepLen)); got != 60 {
		t.Fatalf("expected 60 tool steps retained, got %d", got)
	}
}

// TestCompactProgressWriter_LimitedPlatformTrims verifies that a platform which
// implements MessageSizeLimitProvider (returning 4000, like IM platforms) still
// gets its coalesced progress preview trimmed to stay within the limit.
func TestCompactProgressWriter_LimitedPlatformTrims(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "compact",
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)
	if w.maxChars != 3800 {
		t.Fatalf("maxChars = %d, want 3800 for a 4000-limit platform", w.maxChars)
	}

	// Feed enough content to exceed the 3800-char cap.
	var big strings.Builder
	for i := 0; i < 40; i++ {
		chunk := strings.Repeat("y", 200)
		if !w.AppendEvent(ProgressEntryToolUse, chunk, "", chunk) {
			t.Fatalf("AppendEvent failed at step %d", i)
		}
		big.WriteString(chunk)
	}

	edits := p.getPreviewEdits()
	if len(edits) == 0 {
		t.Fatal("expected at least one preview edit")
	}
	last := edits[len(edits)-1]
	// Trimming keeps a 3800-rune tail plus a "…\n" prefix marker.
	if !strings.HasPrefix(last, "…") {
		t.Fatalf("limited platform preview should be truncated (start with …), got %q", last[:min(20, len(last))])
	}
	if runes := len([]rune(last)); runes > 3803 {
		t.Fatalf("limited platform preview exceeded cap: %d runes", runes)
	}
}


func TestCompactProgressWriter_UsesReplyContextHints(t *testing.T) {
	p := &previewCapturePlatform{}
	replyCtx := progressHintReplyCtx{
		style:   ProgressStyleCard,
		payload: true,
	}

	w := newCompactProgressWriter(context.Background(), p, replyCtx, "codex", LangEnglish, nil)
	if !w.enabled {
		t.Fatal("progress writer should be enabled")
	}
	if !w.usePayload {
		t.Fatal("progress writer should use payload when reply context advertises it")
	}
	if got := w.style; got != ProgressStyleCard {
		t.Fatalf("style = %q, want %q", got, ProgressStyleCard)
	}

	if !w.AppendEvent(ProgressEntryThinking, "planning bridge progress", "", "planning bridge progress") {
		t.Fatal("AppendEvent() = false, want true")
	}
	if len(p.started) != 1 {
		t.Fatalf("started = %d, want 1", len(p.started))
	}
	if !strings.HasPrefix(p.started[0], ProgressCardPayloadPrefix) {
		t.Fatalf("preview start payload = %q, want progress payload prefix", p.started[0])
	}

	if !w.Finalize(ProgressCardStateCompleted) {
		t.Fatal("Finalize() = false, want true")
	}
	if len(p.updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(p.updated))
	}

	parsed, ok := ParseProgressCardPayload(p.updated[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload() failed for %q", p.updated[0])
	}
	if parsed.State != ProgressCardStateCompleted {
		t.Fatalf("state = %q, want %q", parsed.State, ProgressCardStateCompleted)
	}
}

func TestBuildAndParseProgressCardPayloadV2(t *testing.T) {
	payload := BuildProgressCardPayloadV2([]ProgressCardEntry{
		{Kind: ProgressEntryThinking, Text: " plan "},
		{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "pwd"},
	}, false, "Codex", LangChinese, ProgressCardStateRunning)
	if payload == "" {
		t.Fatal("BuildProgressCardPayloadV2 returned empty string")
	}

	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("ParseProgressCardPayload should succeed, payload=%q", payload)
	}
	if parsed.Version != 2 {
		t.Fatalf("version = %d, want 2", parsed.Version)
	}
	if parsed.Agent != "Codex" {
		t.Fatalf("agent = %q, want Codex", parsed.Agent)
	}
	if parsed.Lang != string(LangChinese) {
		t.Fatalf("lang = %q, want %q", parsed.Lang, LangChinese)
	}
	if parsed.State != ProgressCardStateRunning {
		t.Fatalf("state = %q, want %q", parsed.State, ProgressCardStateRunning)
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(parsed.Items))
	}
	if parsed.Items[1].Kind != ProgressEntryToolUse || parsed.Items[1].Tool != "Bash" {
		t.Fatalf("items[1] = %#v, want tool_use/Bash", parsed.Items[1])
	}
}

func TestParseProgressCardPayloadRejectsInvalid(t *testing.T) {
	if _, ok := ParseProgressCardPayload("plain text"); ok {
		t.Fatal("expected parse failure for plain text")
	}
	if _, ok := ParseProgressCardPayload(ProgressCardPayloadPrefix + "{not-json"); ok {
		t.Fatal("expected parse failure for invalid json")
	}
	if _, ok := ParseProgressCardPayload(ProgressCardPayloadPrefix + `{"entries":[]}`); ok {
		t.Fatal("expected parse failure for empty entries")
	}
}

func TestCompactProgressWriter_AppliesTransformToCardPayloadEntries(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "codex", LangEnglish, func(s string) string {
		return strings.ReplaceAll(s, "/root/code/demo/src/app.ts:42", "📄 `src/app.ts:42`")
	})

	if ok := w.AppendStructured(ProgressCardEntry{
		Kind: ProgressEntryThinking,
		Text: "Inspect /root/code/demo/src/app.ts:42",
	}, "Inspect /root/code/demo/src/app.ts:42"); !ok {
		t.Fatal("AppendStructured() = false, want true")
	}

	starts := p.getPreviewStarts()
	if len(starts) != 1 {
		t.Fatalf("preview starts = %d, want 1", len(starts))
	}
	payload, ok := ParseProgressCardPayload(starts[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload(%q) failed", starts[0])
	}
	if len(payload.Items) != 1 {
		t.Fatalf("payload items = %d, want 1", len(payload.Items))
	}
	if got := payload.Items[0].Text; got != "Inspect 📄 `src/app.ts:42`" {
		t.Fatalf("payload item text = %q, want transformed text", got)
	}
}

type stubThrottledProgressPlatform struct {
	stubCompactProgressPlatform
	throttle time.Duration
}

func (p *stubThrottledProgressPlatform) ProgressUpdateInterval() time.Duration {
	return p.throttle
}

func TestCompactProgressWriter_ThrottlesRapidUpdates(t *testing.T) {
	p := &stubThrottledProgressPlatform{
		stubCompactProgressPlatform: stubCompactProgressPlatform{
			stubPlatformEngine: stubPlatformEngine{n: "discord"},
			style:              "card",
			supportPayload:     true,
		},
		throttle: 50 * time.Millisecond,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)

	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "step 1"}, "step 1")
	if len(p.getPreviewStarts()) != 1 {
		t.Fatal("first update should create the preview message")
	}

	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolUse, Tool: "Bash", Text: "pwd"}, "pwd")
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolResult, Tool: "Bash", Text: "ok"}, "ok")
	editsBeforeThrottle := len(p.getPreviewEdits())
	if editsBeforeThrottle > 0 {
		t.Fatalf("rapid updates within throttle window should be skipped, got %d edits", editsBeforeThrottle)
	}

	time.Sleep(60 * time.Millisecond)
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryThinking, Text: "step 4"}, "step 4")
	editsAfterWait := len(p.getPreviewEdits())
	if editsAfterWait != 1 {
		t.Fatalf("update after throttle interval should go through, got %d edits", editsAfterWait)
	}

	ok := w.Finalize(ProgressCardStateCompleted)
	if !ok {
		t.Fatal("Finalize should succeed")
	}
	finalEdits := p.getPreviewEdits()
	last := finalEdits[len(finalEdits)-1]
	payload, parsed := ParseProgressCardPayload(last)
	if !parsed {
		t.Fatalf("final edit should be a valid payload, got %q", last)
	}
	if payload.State != ProgressCardStateCompleted {
		t.Fatalf("state = %q, want completed", payload.State)
	}
	if len(payload.Items) != 4 {
		t.Fatalf("items = %d, want 4 (all buffered items)", len(payload.Items))
	}
}

func TestCompactProgressWriter_DoesNotTransformToolResults(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "codex", LangEnglish, func(s string) string {
		return strings.ReplaceAll(s, "/root/code/demo/src/app.ts:42", "📄 `src/app.ts:42`")
	})

	raw := "/root/code/demo/src/app.ts:42"
	if ok := w.AppendStructured(ProgressCardEntry{
		Kind: ProgressEntryToolResult,
		Text: raw,
	}, raw); !ok {
		t.Fatal("AppendStructured() = false, want true")
	}

	starts := p.getPreviewStarts()
	if len(starts) != 1 {
		t.Fatalf("preview starts = %d, want 1", len(starts))
	}
	payload, ok := ParseProgressCardPayload(starts[0])
	if !ok {
		t.Fatalf("ParseProgressCardPayload(%q) failed", starts[0])
	}
	if got := payload.Items[0].Text; got != raw {
		t.Fatalf("tool result text = %q, want raw %q", got, raw)
	}
}

// maxEntriesHintReplyCtx only advertises a progress entry cap override.
type maxEntriesHintReplyCtx struct{ maxEntries int }

func (r maxEntriesHintReplyCtx) ProgressMaxEntriesHint() int { return r.maxEntries }

// TestCompactProgressWriter_MaxEntriesHintUnlimited verifies that a reply
// target declaring progress_max_entries=0 (e.g. the Web admin bridge adapter)
// keeps every progress entry — the default 10-entry cap exists only to bound
// IM message size, which does not apply to WebSocket streaming.
func TestCompactProgressWriter_MaxEntriesHintUnlimited(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "bridge"},
		style:              "card",
	}
	w := newCompactProgressWriter(context.Background(), p, maxEntriesHintReplyCtx{maxEntries: 0}, "cc", LangEnglish, nil)
	if !w.enabled {
		t.Fatal("progress writer should be enabled for card style")
	}
	if w.maxEntries != 0 {
		t.Fatalf("maxEntries = %d, want 0 (unlimited) per reply hint", w.maxEntries)
	}

	for i := 0; i < 15; i++ {
		entry := fmt.Sprintf("entry-%02d", i)
		if !w.AppendEvent(ProgressEntryInfo, entry, "", entry) {
			t.Fatalf("AppendEvent failed at step %d", i)
		}
	}
	edits := p.getPreviewEdits()
	if len(edits) == 0 {
		t.Fatal("expected at least one preview edit")
	}
	last := edits[len(edits)-1]
	if strings.Contains(last, "Showing latest updates only.") {
		t.Fatal("unlimited hint should not mark progress as truncated")
	}
	for i := 0; i < 15; i++ {
		if !strings.Contains(last, fmt.Sprintf("entry-%02d", i)) {
			t.Fatalf("expected entry-%02d to be retained in preview", i)
		}
	}
}

// TestCompactProgressWriter_MaxEntriesHintCapNumbersWithOffset verifies that a
// capped window keeps only the latest entries and numbers the visible entries
// with their true sequence position (dropped offset included) instead of
// restarting at 1 — so "9. Tool #18" style mismatches cannot recur.
func TestCompactProgressWriter_MaxEntriesHintCapNumbersWithOffset(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "bridge"},
		style:              "card",
	}
	w := newCompactProgressWriter(context.Background(), p, maxEntriesHintReplyCtx{maxEntries: 3}, "cc", LangEnglish, nil)
	if w.maxEntries != 3 {
		t.Fatalf("maxEntries = %d, want 3 per reply hint", w.maxEntries)
	}

	for i := 1; i <= 5; i++ {
		entry := fmt.Sprintf("entry-%d", i)
		if !w.AppendEvent(ProgressEntryInfo, entry, "", entry) {
			t.Fatalf("AppendEvent failed at step %d", i)
		}
	}
	edits := p.getPreviewEdits()
	if len(edits) == 0 {
		t.Fatal("expected at least one preview edit")
	}
	last := edits[len(edits)-1]

	if !strings.Contains(last, "Showing latest updates only.") {
		t.Fatal("capped window should be marked truncated")
	}
	// Only the latest 3 entries survive.
	for _, keep := range []string{"entry-3", "entry-4", "entry-5"} {
		if !strings.Contains(last, keep) {
			t.Fatalf("expected %q retained, got:\n%s", keep, last)
		}
	}
	if strings.Contains(last, "entry-1") || strings.Contains(last, "entry-2") {
		t.Fatalf("expected oldest entries evicted, got:\n%s", last)
	}
	// Visible entries are numbered with the true sequence: 3. 4. 5.
	if !strings.Contains(last, "\n3. entry-3") || !strings.Contains(last, "\n4. entry-4") || !strings.Contains(last, "\n5. entry-5") {
		t.Fatalf("expected offset numbering (3./4./5.), got:\n%s", last)
	}
	if strings.Contains(last, "\n1. ") {
		t.Fatalf("numbering restarted at 1 after trim, got:\n%s", last)
	}
}

// TestCompactProgressWriter_NoHintKeepsDefaultCap verifies the default
// 10-entry cap still applies when the reply target provides no hint.
func TestCompactProgressWriter_NoHintKeepsDefaultCap(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)
	if w.maxEntries != 10 {
		t.Fatalf("maxEntries = %d, want default 10 without hint", w.maxEntries)
	}
}
