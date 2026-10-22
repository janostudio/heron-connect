package bridge

import (
	"context"
	"testing"
)

func TestBridgePlatform_DeclaresStreamPreviewMode(t *testing.T) {
	bp := &BridgePlatform{}
	if got := bp.StreamPreviewMode(); got != "tool_hold" {
		t.Fatalf("StreamPreviewMode() = %q, want %q", got, "tool_hold")
	}
}

// The engine rewrites the preview body wholesale on every text chunk, so tool
// lines have to be re-attached on each update instead of being written once.
func TestBridgePlatform_MergeToolProgressSurvivesBodyRewrite(t *testing.T) {
	bp := &BridgePlatform{}

	if got := bp.mergeToolProgress("h1", "第一段"); got != "第一段" {
		t.Fatalf("first body = %q, want 第一段", got)
	}

	bp.progressMu.Lock()
	bp.progress["h1"].lines = append(bp.progress["h1"].lines, "· read repos")
	bp.progressMu.Unlock()

	got := bp.mergeToolProgress("h1", "第二段")
	want := "· read repos\n\n第二段"
	if got != want {
		t.Fatalf("rewritten body = %q, want %q", got, want)
	}
}

func TestBridgePlatform_ClearProgressDropsLines(t *testing.T) {
	bp := &BridgePlatform{}
	bp.mergeToolProgress("h1", "正文")
	bp.progressMu.Lock()
	bp.progress["h1"].lines = append(bp.progress["h1"].lines, "· grep foo")
	bp.progressMu.Unlock()

	bp.clearProgress("h1")

	bp.progressMu.Lock()
	_, exists := bp.progress["h1"]
	bp.progressMu.Unlock()
	if exists {
		t.Fatal("clearProgress left the entry behind")
	}

	// A later body update for the same handle must start clean.
	if got := bp.mergeToolProgress("h1", "新一轮"); got != "新一轮" {
		t.Fatalf("body after clear = %q, want 新一轮", got)
	}
}

// A tool line arriving before any body exists is only recorded: pushing a
// preview that holds nothing but the line shows an empty bubble that the first
// text chunk would immediately replace.
func TestBridgePlatform_OnToolStartWithoutBodySkipsUpdate(t *testing.T) {
	bp := &BridgePlatform{}
	// No server: an attempt to update would panic, which is the assertion here.
	rc := &bridgeReplyCtx{Platform: "bridge", ClientID: "c1", SessionKey: "bridge:s:1", ReplyCtx: "h1"}

	if err := bp.OnToolStart(context.Background(), rc, "read", "repos/heron-ai", ""); err != nil {
		t.Fatalf("OnToolStart() error = %v", err)
	}

	bp.progressMu.Lock()
	lines := bp.progress["h1"].lines
	bp.progressMu.Unlock()
	if len(lines) != 1 || lines[0] != "· read repos/heron-ai" {
		t.Fatalf("recorded lines = %v", lines)
	}

	if got := bp.mergeToolProgress("h1", "正文"); got != "· read repos/heron-ai\n\n正文" {
		t.Fatalf("merged body = %q", got)
	}
}

func TestFormatToolProgressLine(t *testing.T) {
	cases := []struct{ tool, arg, want string }{
		{"read", "repos/a", "· read repos/a"},
		{"read", "", "· read"},
		{"", "repos/a", "· repos/a"},
		{"", "", "·"},
	}
	for _, c := range cases {
		if got := formatToolProgressLine(c.tool, c.arg); got != c.want {
			t.Errorf("formatToolProgressLine(%q, %q) = %q, want %q", c.tool, c.arg, got, c.want)
		}
	}
}
