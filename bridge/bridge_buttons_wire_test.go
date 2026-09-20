package bridge

import (
	"encoding/json"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

// The Web client reads button fields as `text` / `data` (see
// web/src/pages/Chat/chatSessionsCore.ts, which stores `btns.buttons` straight
// onto the message and renders `btn.text` / `btn.data`).
//
// ButtonOption carries no JSON tags, so a plain Marshal emits the Go field
// names ("Text"/"Data") and every button reaches the browser with an undefined
// label — the permission prompt rendered as unlabelled green pills, making it
// impossible to tell 允许 from 拒绝. These tests pin the wire format so a
// future tag change cannot silently reintroduce a label-less button.
func TestButtonOption_WireFormatIsLowercase(t *testing.T) {
	buttons := [][]core.ButtonOption{
		{
			{Text: "允许", Data: "perm:allow"},
			{Text: "拒绝", Data: "perm:deny"},
		},
		{
			{Text: "允许所有 (本次会话)", Data: "perm:allow_all"},
		},
	}

	// The exact payload SendWithButtons puts on the wire.
	out, err := json.Marshal(map[string]any{
		"type":    "buttons",
		"content": "Agent 想要使用 ExitPlanMode",
		"buttons": buttons,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Buttons [][]map[string]any `json:"buttons"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Buttons) != 2 {
		t.Fatalf("rows = %d, want 2", len(decoded.Buttons))
	}

	// Row 0: allow/deny must both carry a readable label.
	for i, want := range []string{"允许", "拒绝"} {
		btn := decoded.Buttons[0][i]
		got, ok := btn["text"].(string)
		if !ok || got == "" {
			t.Errorf("row0 btn%d: missing lowercase %q (got %v) — the Web client reads btn.text, so a missing tag renders a blank button", i, "text", btn)
			continue
		}
		if got != want {
			t.Errorf("row0 btn%d text = %q, want %q", i, got, want)
		}
		if data, _ := btn["data"].(string); data == "" {
			t.Errorf("row0 btn%d: missing lowercase %q", i, "data")
		}
	}

	// Row 1: the allow-all button, same contract.
	if got, _ := decoded.Buttons[1][0]["text"].(string); got != "允许所有 (本次会话)" {
		t.Errorf("row1 btn0 text = %q, want 允许所有 (本次会话)", got)
	}

	// Guard against the regression specifically: Go field names must not leak.
	if _, leaked := decoded.Buttons[0][0]["Text"]; leaked {
		t.Error(`payload contains capitalized "Text" — ButtonOption is missing its json:"text" tag`)
	}
	if _, leaked := decoded.Buttons[0][0]["Data"]; leaked {
		t.Error(`payload contains capitalized "Data" — ButtonOption is missing its json:"data" tag`)
	}
}
