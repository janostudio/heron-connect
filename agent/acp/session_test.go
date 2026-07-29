package acp

import (
	"encoding/json"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

const usageUpdateWithMeta = `{
  "sessionUpdate": "usage_update",
  "used": 48152,
  "size": 1000000,
  "cost": {"amount": 29.15, "currency": ""},
  "_meta": {
    "usage": {
      "prompt_tokens": 48152,
      "completion_tokens": 163,
      "total_tokens": 48315
    }
  }
}`

const usageUpdateLegacy = `{
  "sessionUpdate": "usage_update",
  "used": 1000,
  "size": 100000
}`

const notUsageUpdate = `{
  "sessionUpdate": "session_info_update",
  "used": 0,
  "size": 1000000
}`

func wrapUsageUpdate(updateJSON string) json.RawMessage {
	params := map[string]any{
		"sessionId": "test-session-id",
		"update":    json.RawMessage(updateJSON),
	}
	b, _ := json.Marshal(params)
	return json.RawMessage(b)
}

func TestMaybeAbsorbUsageUpdate_ParsesMetaUsage(t *testing.T) {
	s := &acpSession{}
	params := wrapUsageUpdate(usageUpdateWithMeta)
	s.maybeAbsorbUsageUpdate(params)

	s.usageMu.RLock()
	snap := s.usageSnapshot
	s.usageMu.RUnlock()

	if snap == nil {
		t.Fatal("expected non-nil usageSnapshot after usage_update")
	}
	if snap.UsedTokens != 48152 {
		t.Fatalf("expected UsedTokens=48152, got %d", snap.UsedTokens)
	}
	if snap.ContextWindow != 1000000 {
		t.Fatalf("expected ContextWindow=1000000, got %d", snap.ContextWindow)
	}
	if snap.InputTokens != 48152 {
		t.Fatalf("expected InputTokens=48152, got %d", snap.InputTokens)
	}
	if snap.OutputTokens != 163 {
		t.Fatalf("expected OutputTokens=163, got %d", snap.OutputTokens)
	}
	if snap.TotalTokens != 48315 {
		t.Fatalf("expected TotalTokens=48315, got %d", snap.TotalTokens)
	}
}

func TestMaybeAbsorbUsageUpdate_WithoutMetaUsage(t *testing.T) {
	s := &acpSession{}
	params := wrapUsageUpdate(usageUpdateLegacy)
	s.maybeAbsorbUsageUpdate(params)

	s.usageMu.RLock()
	snap := s.usageSnapshot
	s.usageMu.RUnlock()

	if snap == nil {
		t.Fatal("expected non-nil usageSnapshot after usage_update")
	}
	if snap.UsedTokens != 1000 {
		t.Fatalf("expected UsedTokens=1000, got %d", snap.UsedTokens)
	}
	if snap.InputTokens != 0 {
		t.Fatalf("expected InputTokens=0 without _meta.usage, got %d", snap.InputTokens)
	}
	if snap.OutputTokens != 0 {
		t.Fatalf("expected OutputTokens=0 without _meta.usage, got %d", snap.OutputTokens)
	}
	// TotalTokens falls back to Used when _meta.usage.total_tokens is absent
	if snap.TotalTokens != 1000 {
		t.Fatalf("expected TotalTokens=1000 (fallback to Used), got %d", snap.TotalTokens)
	}
}

func TestMaybeAbsorbUsageUpdate_NotUsageUpdate(t *testing.T) {
	s := &acpSession{}
	// Set a known snapshot first
	s.usageMu.Lock()
	s.usageSnapshot = &core.ContextUsage{UsedTokens: 500}
	s.usageMu.Unlock()

	params := wrapUsageUpdate(notUsageUpdate)
	s.maybeAbsorbUsageUpdate(params)

	s.usageMu.RLock()
	snap := s.usageSnapshot
	s.usageMu.RUnlock()

	if snap.UsedTokens != 500 {
		t.Fatalf("expected snapshot unchanged, got UsedTokens=%d", snap.UsedTokens)
	}
}

func TestMaybeAbsorbUsageUpdate_ZeroTotalTokensFallback(t *testing.T) {
	update := `{
    "sessionUpdate": "usage_update",
    "used": 5000,
    "size": 100000,
    "_meta": {
      "usage": {
        "prompt_tokens": 5000,
        "completion_tokens": 100,
        "total_tokens": 0
      }
    }
  }`
	s := &acpSession{}
	params := wrapUsageUpdate(update)
	s.maybeAbsorbUsageUpdate(params)

	s.usageMu.RLock()
	snap := s.usageSnapshot
	s.usageMu.RUnlock()

	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	// total_tokens=0 should not override Used
	if snap.TotalTokens != 5000 {
		t.Fatalf("expected TotalTokens=5000 (fallback to Used when total_tokens=0), got %d", snap.TotalTokens)
	}
	if snap.InputTokens != 5000 {
		t.Fatalf("expected InputTokens=5000, got %d", snap.InputTokens)
	}
}
