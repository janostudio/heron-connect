package core

import (
	"testing"
	"testing/fstest"
)

// resetWebAssets resets the package-level web assets FS to nil, restoring the
// default "no web UI embedded" state so tests are isolated from each other.
func resetWebAssets() {
	webAssetsFS = nil
}

func TestCmdWebStatus_WebAssetsUnavailable(t *testing.T) {
	resetWebAssets()
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.cmdWebStatus(p, &Message{ReplyCtx: "ctx"})

	got := p.getSent()
	if len(got) != 1 || got[0] != "⚠️ Web admin is not available in this build. Rebuild without the `no_web` tag to enable it." {
		t.Fatalf("reply = %#v, want MsgWebNotSupported", got)
	}
}

func TestCmdWebStatus_NilStatusFunc(t *testing.T) {
	// Web assets available, but no status callback wired up.
	RegisterWebAssets(fstest.MapFS{"index.html": &fstest.MapFile{}})
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	e.cmdWebStatus(p, &Message{ReplyCtx: "ctx"})

	got := p.getSent()
	if len(got) != 1 || got[0] != "⚠️ Web admin is not available in this build. Rebuild without the `no_web` tag to enable it." {
		t.Fatalf("reply = %#v, want MsgWebNotSupported", got)
	}
}

func TestCmdWebStatus_NotEnabled(t *testing.T) {
	RegisterWebAssets(fstest.MapFS{"index.html": &fstest.MapFile{}})
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetWebStatusFunc(func() string { return "" })

	e.cmdWebStatus(p, &Message{ReplyCtx: "ctx"})

	got := p.getSent()
	want := "ℹ️ Web admin is not enabled.\n\nUse `/web setup` to configure and enable it."
	if len(got) != 1 || got[0] != want {
		t.Fatalf("reply = %#v, want MsgWebNotEnabled", got)
	}
}

func TestCmdWebStatus_Enabled(t *testing.T) {
	RegisterWebAssets(fstest.MapFS{"index.html": &fstest.MapFile{}})
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetWebStatusFunc(func() string { return "http://localhost:9820" })

	e.cmdWebStatus(p, &Message{ReplyCtx: "ctx"})

	got := p.getSent()
	want := "🌐 **Web Admin**\n\nURL: http://localhost:9820"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("reply = %#v, want MsgWebStatus with url", got)
	}
}

func TestCmdWeb_SubcommandDispatch(t *testing.T) {
	RegisterWebAssets(fstest.MapFS{"index.html": &fstest.MapFile{}})
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	statusCalls := 0
	setupCalls := 0
	e.SetWebStatusFunc(func() string {
		statusCalls++
		return "http://localhost:9820"
	})
	e.SetWebSetupFunc(func() (int, string, bool, error) {
		setupCalls++
		return 9820, "tok", false, nil
	})

	// No args → default to status.
	e.cmdWeb(p, &Message{ReplyCtx: "ctx"}, nil)
	if statusCalls != 1 || setupCalls != 0 {
		t.Fatalf("no-arg dispatch: statusCalls=%d setupCalls=%d, want status only", statusCalls, setupCalls)
	}

	// Explicit "status".
	p.clearSent()
	e.cmdWeb(p, &Message{ReplyCtx: "ctx"}, []string{"status"})
	if statusCalls != 2 || setupCalls != 0 {
		t.Fatalf("status dispatch: statusCalls=%d setupCalls=%d, want status only", statusCalls, setupCalls)
	}

	// "setup" subcommand.
	p.clearSent()
	e.cmdWeb(p, &Message{ReplyCtx: "ctx"}, []string{"setup"})
	if setupCalls != 1 || statusCalls != 2 {
		t.Fatalf("setup dispatch: statusCalls=%d setupCalls=%d, want setup only", statusCalls, setupCalls)
	}
}

func TestCmdWebSetup_NilSetupFunc(t *testing.T) {
	RegisterWebAssets(fstest.MapFS{"index.html": &fstest.MapFile{}})
	defer resetWebAssets()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	// No setup callback wired.
	e.cmdWebSetup(p, &Message{ReplyCtx: "ctx"})

	got := p.getSent()
	if len(got) != 1 || got[0] != "⚠️ Web admin is not available in this build. Rebuild without the `no_web` tag to enable it." {
		t.Fatalf("reply = %#v, want MsgWebNotSupported", got)
	}
}
