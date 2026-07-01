package wecom

import (
	"strings"
	"testing"
)

// These tests define the contract for the new three-region wecomStreamAssembler.
// They are the P0/P1 tests from wecom-stream-tdd-gap-analysis.md:
//   - Invariants I1-I6 (section 3.2 of the design doc)
//   - Paths G1-G6 (section 3.1 of the TDD gap analysis)
//
// In the Red phase, all tests should fail because the implementation is a stub.
// In the Green phase, the implementation is filled in to make all tests pass.

// --- I1: visibleText only contains model text, never tool messages ---

// TestAppendText_DoesNotTouchProgressLines verifies I1:
// appendText only writes visibleText; progressLines stays empty.
func TestAppendText_DoesNotTouchProgressLines(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("hello")
	a.appendText(" world")

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.progressLines) != 0 {
		t.Fatalf("progressLines = %v, want empty (appendText must not touch progress)", a.progressLines)
	}
	if a.heldTool != "" {
		t.Fatalf("heldTool = %q, want empty (appendText must not touch heldTool)", a.heldTool)
	}
}

// TestOnToolStart_DoesNotTouchVisibleText verifies I1:
// onToolStart only writes progressLines + heldTool; visibleText stays empty.
func TestOnToolStart_DoesNotTouchVisibleText(t *testing.T) {
	a := newWecomStreamAssembler()
	a.onToolStart("Bash", "run tests", "npm test")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.visibleText != "" {
		t.Fatalf("visibleText = %q, want empty (onToolStart must not touch visibleText)", a.visibleText)
	}
}

// --- I3: heldTool only retains the latest tool block ---

// TestOnToolStart_OverwritesHeldTool verifies I3:
// multiple onToolStart calls only keep the latest heldTool.
func TestOnToolStart_OverwritesHeldTool(t *testing.T) {
	a := newWecomStreamAssembler()
	a.onToolStart("Bash", "first", "cmd1")
	a.onToolStart("Bash", "second", "cmd2")
	a.onToolStart("Bash", "third", "cmd3")

	a.mu.Lock()
	defer a.mu.Unlock()
	if !strings.Contains(a.heldTool, "third") {
		t.Fatalf("heldTool = %q, want contain 'third' (latest overwrites previous)", a.heldTool)
	}
}

// --- I5: finish() clears progressLines and heldTool ---

// TestFinish_ClearsProgressLinesAndHeldTool verifies I5:
// after finish, progressLines is nil and heldTool is empty.
func TestFinish_ClearsProgressLinesAndHeldTool(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("partial answer")
	a.onToolStart("Bash", "run tests", "npm test")
	a.onToolComplete("Bash", "all passed")

	a.finish("final answer")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.progressLines != nil {
		t.Fatalf("progressLines = %v, want nil after finish", a.progressLines)
	}
	if a.heldTool != "" {
		t.Fatalf("heldTool = %q, want empty after finish", a.heldTool)
	}
	if !a.finished {
		t.Fatalf("finished = false, want true after finish")
	}
	if a.visibleText != "final answer" {
		t.Fatalf("visibleText = %q, want 'final answer'", a.visibleText)
	}
}

// TestFinish_EmptyFinalText_KeepsVisibleText verifies that finish with empty
// finalText does not wipe already-accumulated visibleText.
func TestFinish_EmptyFinalText_KeepsVisibleText(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("accumulated text")
	a.onToolStart("Bash", "run", "cmd")

	a.finish("")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.visibleText != "accumulated text" {
		t.Fatalf("visibleText = %q, want 'accumulated text' (empty finish should keep prior text)", a.visibleText)
	}
	if a.progressLines != nil {
		t.Fatalf("progressLines = %v, want nil (finish always clears progress)", a.progressLines)
	}
}

// --- I4: render() is read-only ---

// TestRender_IsReadOnly_Idempotent verifies I4:
// calling render() multiple times produces identical results and does not
// mutate any state field.
func TestRender_IsReadOnly_Idempotent(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("some text")
	a.onToolStart("Bash", "run tests", "npm test")

	first := a.snapshot()
	second := a.snapshot()
	third := a.snapshot()

	if first != second || second != third {
		t.Fatalf("render() not idempotent: first=%q second=%q third=%q", first, second, third)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// State should be unchanged by snapshot calls
	if a.visibleText != "some text" {
		t.Fatalf("visibleText changed after snapshot: %q", a.visibleText)
	}
	if len(a.progressLines) != 1 {
		t.Fatalf("progressLines changed after snapshot: %v", a.progressLines)
	}
}

// --- G1/G2: appendText and onToolStart region isolation ---

// TestRender_ProgressAndTextSeparated verifies G3:
// render() produces progress lines and visible text separated by the separator.
func TestRender_ProgressAndTextSeparated(t *testing.T) {
	a := newWecomStreamAssembler()
	a.onToolStart("Bash", "run tests", "npm test")
	a.appendText("answer body")

	rendered := a.snapshot()

	// Both parts should be present
	if !strings.Contains(rendered, "🛠️") {
		t.Fatalf("rendered = %q, want contain tool progress line", rendered)
	}
	if !strings.Contains(rendered, "answer body") {
		t.Fatalf("rendered = %q, want contain visible text", rendered)
	}
	// Progress should come before visible text
	toolIdx := strings.Index(rendered, "🛠️")
	textIdx := strings.Index(rendered, "answer body")
	if toolIdx < 0 || textIdx < 0 || toolIdx > textIdx {
		t.Fatalf("rendered = %q, want tool progress before visible text (toolIdx=%d textIdx=%d)", rendered, toolIdx, textIdx)
	}
	// Separator should be present between them
	if !strings.Contains(rendered, "---") {
		t.Fatalf("rendered = %q, want separator between progress and text", rendered)
	}
}

// --- G5: onToolComplete adds ✅ row, does not enter visibleText ---

// TestOnToolComplete_AddsCheckmarkLine verifies G5:
// onToolComplete adds a ✅ line to progressLines; visibleText is untouched.
func TestOnToolComplete_AddsCheckmarkLine(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("answer")
	a.onToolStart("Bash", "run tests", "npm test")
	a.onToolComplete("Bash", "all passed")

	a.mu.Lock()
	defer a.mu.Unlock()
	// Should have a ✅ line in progressLines
	found := false
	for _, line := range a.progressLines {
		if strings.HasPrefix(line, completeMark) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("progressLines = %v, want a ✅ completion line", a.progressLines)
	}
	// visibleText must not contain the result summary
	if strings.Contains(a.visibleText, "all passed") {
		t.Fatalf("visibleText = %q, must not contain tool result 'all passed'", a.visibleText)
	}
}

// --- G6: progressLines bounded by maxProgressLines (FIFO) ---

// TestProgressLines_FIFO_BoundedByMax verifies G6:
// when progressLines exceeds maxProgressLines, oldest lines are dropped.
func TestProgressLines_FIFO_BoundedByMax(t *testing.T) {
	a := newWecomStreamAssembler()
	a.maxProgressLines = 3

	a.onToolStart("Tool1", "a1", "")
	a.onToolStart("Tool2", "a2", "")
	a.onToolStart("Tool3", "a3", "")
	a.onToolStart("Tool4", "a4", "") // should drop Tool1

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.progressLines) != 3 {
		t.Fatalf("progressLines len = %d, want 3 (bounded by max)", len(a.progressLines))
	}
	joined := strings.Join(a.progressLines, "\n")
	if strings.Contains(joined, "Tool1") {
		t.Fatalf("progressLines = %v, oldest (Tool1) should have been dropped (FIFO)", a.progressLines)
	}
	if !strings.Contains(joined, "Tool4") {
		t.Fatalf("progressLines = %v, newest (Tool4) should be present", a.progressLines)
	}
}

// --- I6: same stream_id returns same assembler instance ---

// TestStreamStateFor_ReturnsSameAssembler verifies I6:
// calling streamStateFor twice with the same reqID+streamID returns the same
// wsStreamState (and therefore the same assembler instance).
func TestStreamStateFor_ReturnsSameAssembler(t *testing.T) {
	p := &WSPlatform{streamState: make(map[string]*wsStreamState)}
	rc := wsReplyContext{reqID: "req_dup", streamID: "stream_dup"}

	_, state1, err := p.streamStateFor(rc)
	if err != nil {
		t.Fatalf("first streamStateFor failed: %v", err)
	}
	_, state2, err := p.streamStateFor(rc)
	if err != nil {
		t.Fatalf("second streamStateFor failed: %v", err)
	}
	if state1 != state2 {
		t.Fatalf("streamStateFor returned different state instances for same key: %p vs %p", state1, state2)
	}
}

// --- G1/G2 additional: appendText returns rendered text containing the new text ---

// TestAppendText_ReturnsRenderedContainingNewText verifies that appendText
// returns a render() result that includes the newly appended text.
func TestAppendText_ReturnsRenderedContainingNewText(t *testing.T) {
	a := newWecomStreamAssembler()
	rendered := a.appendText("hello world")
	if !strings.Contains(rendered, "hello world") {
		t.Fatalf("appendText returned %q, want contain 'hello world'", rendered)
	}
}

// --- Tool line formatting ---

// TestFormatToolLine_ExplainMode verifies that explain mode produces a
// compact single-line tool progress entry.
func TestFormatToolLine_ExplainMode(t *testing.T) {
	a := newWecomStreamAssembler()
	a.detailMode = "explain"
	line := a.formatToolLine("Bash", "run tests", "npm test --verbose")
	if !strings.HasPrefix(line, toolPrefix) {
		t.Fatalf("tool line = %q, want prefix %q", line, toolPrefix)
	}
	if !strings.Contains(line, "Bash") {
		t.Fatalf("tool line = %q, want contain tool name 'Bash'", line)
	}
	if !strings.Contains(line, "run tests") {
		t.Fatalf("tool line = %q, want contain explain arg 'run tests'", line)
	}
	// explain mode should NOT include raw arg
	if strings.Contains(line, "npm test --verbose") {
		t.Fatalf("tool line = %q, explain mode should not include raw arg", line)
	}
}

// TestFormatToolLine_RawMode verifies that raw mode appends the raw command.
func TestFormatToolLine_RawMode(t *testing.T) {
	a := newWecomStreamAssembler()
	a.detailMode = "raw"
	line := a.formatToolLine("Bash", "run tests", "npm test --verbose")
	if !strings.Contains(line, "run tests") {
		t.Fatalf("tool line = %q, want contain explain arg", line)
	}
	if !strings.Contains(line, "npm test --verbose") {
		t.Fatalf("tool line = %q, raw mode should include raw arg", line)
	}
}

// TestTruncateMiddle_PreservesHeadAndTail verifies middle-truncation logic.
func TestTruncateMiddle_PreservesHeadAndTail(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz"
	got := truncateMiddle(long, 9) // head=4, tail=4, +1 ellipsis
	if !strings.HasPrefix(got, "abcd") {
		t.Fatalf("truncateMiddle = %q, want head 'abcd'", got)
	}
	if !strings.HasSuffix(got, "wxyz") {
		t.Fatalf("truncateMiddle = %q, want tail 'wxyz'", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("truncateMiddle = %q, want ellipsis in middle", got)
	}
}

// TestTruncateMiddle_ShortStringUnchanged verifies short strings are not truncated.
func TestTruncateMiddle_ShortStringUnchanged(t *testing.T) {
	got := truncateMiddle("abc", 10)
	if got != "abc" {
		t.Fatalf("truncateMiddle = %q, want 'abc' unchanged", got)
	}
}

// TestDiscard_ClearsAll verifies discard() resets all state.
func TestDiscard_ClearsAll(t *testing.T) {
	a := newWecomStreamAssembler()
	a.appendText("text")
	a.onToolStart("Bash", "run", "cmd")
	a.onToolComplete("Bash", "done")

	a.discard()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.visibleText != "" {
		t.Fatalf("visibleText = %q, want empty after discard", a.visibleText)
	}
	if a.progressLines != nil {
		t.Fatalf("progressLines = %v, want nil after discard", a.progressLines)
	}
	if a.heldTool != "" {
		t.Fatalf("heldTool = %q, want empty after discard", a.heldTool)
	}
	if a.finished {
		t.Fatalf("finished = true, want false after discard")
	}
}
