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

// TestCompactProgressWriter_RowMergeKeepsSubagentLabel verifies that an Agent
// row keeps its sub-agent label when its tool_use collapses into a tool_result.
// The result entry carries no Subagent, so the writer must inherit the label
// from the row it replaces — otherwise the Web card would drop
// "Agent · Explore" back to a bare "Agent" the moment the sub-agent finishes.
func TestCompactProgressWriter_RowMergeKeepsSubagentLabel(t *testing.T) {
	p := &stubCompactProgressPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "feishu"},
		style:              "card",
		supportPayload:     true,
	}
	w := newCompactProgressWriter(context.Background(), p, "ctx", "cc", LangEnglish, nil)

	w.AppendStructured(ProgressCardEntry{
		Kind:           ProgressEntryToolUse,
		Text:           "explore the repo",
		Tool:           "Agent",
		ID:             "a1",
		CorrelationKey: "a1",
		Subagent:       "Explore",
	}, "🔧 Agent")

	// tool_result merge: no Subagent on the entry → inherit from the replaced row.
	w.AppendStructured(ProgressCardEntry{
		Kind:           ProgressEntryToolResult,
		Text:           "final report",
		Tool:           "Agent",
		ID:             "a1",
		CorrelationKey: "a1",
		Success:        boolPtr(true),
	}, "✅ Agent")

	if len(w.items) != 1 {
		t.Fatalf("items = %d, want 1 (in-place merge)", len(w.items))
	}
	if w.items[0].Subagent != "Explore" {
		t.Fatalf("subagent = %q after merge, want Explore", w.items[0].Subagent)
	}
	if w.items[0].Kind != ProgressEntryToolResult {
		t.Fatalf("kind = %q after merge, want tool_result", w.items[0].Kind)
	}
	if w.items[0].ID != "a1" {
		t.Fatalf("id = %q after merge, want a1 (children must still find their parent)", w.items[0].ID)
	}
}

// TestBuildProgressCardPayloadV2_CarriesSubagent verifies the payload builder
// preserves the Subagent label on the wire for clients.
func TestBuildProgressCardPayloadV2_CarriesSubagent(t *testing.T) {
	payload := BuildProgressCardPayloadV2([]ProgressCardEntry{
		{Kind: ProgressEntryToolUse, Text: "x", Tool: "Agent", ID: "a1", CorrelationKey: "a1", Subagent: "Explore"},
	}, false, "cc", LangEnglish, ProgressCardStateRunning)
	parsed, ok := ParseProgressCardPayload(payload)
	if !ok {
		t.Fatalf("parse failed: %q", payload)
	}
	if len(parsed.Items) != 1 || parsed.Items[0].Subagent != "Explore" {
		t.Fatalf("items = %+v, want one item with subagent=Explore", parsed.Items)
	}
}
