package core

import (
	"context"
	"testing"
)

// TestCompactProgressWriter_RowMergeByCorrelationKey verifies that two entries
// sharing a CorrelationKey collapse into a single in-place-updated row instead
// of appending a second row — the tool_use → tool_result status flow.
func TestCompactProgressWriter_RowMergeByCorrelationKey(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)
	if !w.enabled {
		t.Fatal("progress writer should be enabled")
	}

	// tool_use row
	ok := w.AppendStructured(ProgressCardEntry{
		Kind:           ProgressEntryToolUse,
		Text:           "running task",
		Tool:           "Bash",
		ID:             "tool-1",
		CorrelationKey: "tool-1",
	}, "🔧 Bash")
	if !ok {
		t.Fatal("AppendStructured(tool_use) = false, want true")
	}
	if len(w.items) != 1 {
		t.Fatalf("items = %d after tool_use, want 1", len(w.items))
	}

	// tool_result row with the same correlation key → merge in place.
	ok = w.AppendStructured(ProgressCardEntry{
		Kind:           ProgressEntryToolResult,
		Text:           "done",
		Tool:           "Bash",
		ID:             "tool-1",
		CorrelationKey: "tool-1",
		Status:         "completed",
	}, "✅ Bash")
	if !ok {
		t.Fatal("AppendStructured(tool_result) = false, want true")
	}
	if len(w.items) != 1 {
		t.Fatalf("items = %d after tool_result merge, want 1 (in-place update)", len(w.items))
	}
	if w.items[0].Kind != ProgressEntryToolResult || w.items[0].Status != "completed" {
		t.Fatalf("merged item = %+v, want tool_result/completed", w.items[0])
	}

	// A different key still appends (no false merge).
	w.AppendStructured(ProgressCardEntry{
		Kind:           ProgressEntryToolUse,
		Text:           "another",
		Tool:           "Read",
		ID:             "tool-2",
		CorrelationKey: "tool-2",
	}, "🔧 Read")
	if len(w.items) != 2 {
		t.Fatalf("items = %d after distinct key, want 2", len(w.items))
	}
}

// TestCompactProgressWriter_NoMergeWithoutCorrelationKey verifies that entries
// WITHOUT a CorrelationKey keep the legacy append behavior (no collapse) — the
// conservative guard against collapsing parallel same-name tools.
func TestCompactProgressWriter_NoMergeWithoutCorrelationKey(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)

	// Two tool_use entries with the same tool name but NO correlation key.
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolUse, Text: "a", Tool: "Bash"}, "🔧 Bash")
	w.AppendStructured(ProgressCardEntry{Kind: ProgressEntryToolUse, Text: "b", Tool: "Bash"}, "🔧 Bash")

	if len(w.items) != 2 {
		t.Fatalf("items = %d without correlation key, want 2 (no merge)", len(w.items))
	}
}

// TestCompactProgressWriter_FindCorrelationKey_ReverseMostRecent verifies the
// reverse-search returns the most recent matching row index.
func TestCompactProgressWriter_FindCorrelationKey_ReverseMostRecent(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)

	w.items = []ProgressCardEntry{
		{CorrelationKey: "k1"},
		{CorrelationKey: "k2"},
		{CorrelationKey: "k1"}, // most recent k1
	}

	if idx := w.findCorrelationKey("k1"); idx != 2 {
		t.Fatalf("findCorrelationKey(k1) = %d, want 2 (most recent)", idx)
	}
	if idx := w.findCorrelationKey("k2"); idx != 1 {
		t.Fatalf("findCorrelationKey(k2) = %d, want 1", idx)
	}
	if idx := w.findCorrelationKey("missing"); idx != -1 {
		t.Fatalf("findCorrelationKey(missing) = %d, want -1", idx)
	}
}

// TestBuildProgressCardPayloadV2_CarriesCorrelationKey verifies the payload
// builder preserves CorrelationKey on the wire for clients.
func TestBuildProgressCardPayloadV2_CarriesCorrelationKey(t *testing.T) {
	payload := BuildProgressCardPayloadV2([]ProgressCardEntry{
		{Kind: ProgressEntryToolUse, Text: "x", Tool: "Bash", ID: "t1", CorrelationKey: "t1"},
	}, false, "cc", LangEnglish, ProgressCardStateRunning)
	if payload == "" {
		t.Fatal("empty payload")
	}
	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("parse failed: %q", payload)
	}
	if len(parsed.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(parsed.Items))
	}
	if parsed.Items[0].CorrelationKey != "t1" {
		t.Fatalf("correlation_key = %q, want t1", parsed.Items[0].CorrelationKey)
	}
}
