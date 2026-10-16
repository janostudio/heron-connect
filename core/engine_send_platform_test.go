package core

import (
	"errors"
	"testing"
)

// stubExplicitPlatform is a plain platform that supports proactive sends by
// reconstructing a reply context from any key with its own prefix.
type stubExplicitPlatform struct {
	stubPlatformEngine
	reconstructKey string
	reconstructErr error
}

func (p *stubExplicitPlatform) ReconstructReplyCtx(sessionKey string) (any, error) {
	if p.reconstructErr != nil {
		return nil, p.reconstructErr
	}
	p.reconstructKey = sessionKey
	return "explicit-rctx", nil
}

func (p *stubExplicitPlatform) getReconstructKey() string {
	return p.reconstructKey
}

// TestSendToSessionOpts_ExplicitPlatformOverridesBinding: a wecom:... key that
// is currently bound to bridge must deliver via wecom when platform is forced.
func TestSendToSessionOpts_ExplicitPlatformOverridesBinding(t *testing.T) {
	bridge := &stubPlatformEngine{n: "bridge"}
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{bridge, wecom}, "", LangEnglish)
	e.interactiveStates["wecom:C1:U1"] = &interactiveState{
		platform: bridge,
		replyCtx: "bridge-ctx",
	}

	platform, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "back to wecom", nil, nil, SendOptions{Platform: "wecom"},
	)
	if err != nil {
		t.Fatalf("SendToSessionOptsWithAttachments returned error: %v", err)
	}
	if platform != "wecom" {
		t.Fatalf("platform = %q, want wecom", platform)
	}
	if got := bridge.getSent(); len(got) != 0 {
		t.Fatalf("bridge received %#v, want nothing", got)
	}
	if got := wecom.getSent(); len(got) != 1 || got[0] != "back to wecom" {
		t.Fatalf("wecom sent = %#v, want one message", got)
	}
}

// TestSendToSessionOpts_OriginIgnoresBinding: "origin" must route by the key
// prefix even when the key is bound to another platform.
func TestSendToSessionOpts_OriginIgnoresBinding(t *testing.T) {
	bridge := &stubPlatformEngine{n: "bridge"}
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{bridge, wecom}, "", LangEnglish)
	e.interactiveStates["wecom:C1:U1"] = &interactiveState{
		platform: bridge,
		replyCtx: "bridge-ctx",
	}

	platform, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "back to origin", nil, nil, SendOptions{Platform: "origin"},
	)
	if err != nil {
		t.Fatalf("SendToSessionOptsWithAttachments returned error: %v", err)
	}
	if platform != "wecom" {
		t.Fatalf("platform = %q, want wecom", platform)
	}
	if got := bridge.getSent(); len(got) != 0 {
		t.Fatalf("bridge received %#v, want nothing", got)
	}
	if got := wecom.getSent(); len(got) != 1 {
		t.Fatalf("wecom sent = %#v, want one message", got)
	}
}

// TestSendToSessionOpts_WebAliasForBridge.
func TestSendToSessionOpts_WebAliasForBridge(t *testing.T) {
	bridge := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "bridge"}}
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{bridge, wecom}, "", LangEnglish)

	platform, err := e.SendToSessionOptsWithAttachments(
		"bridge:web-admin:test:conv-1", "to web", nil, nil, SendOptions{Platform: "web"},
	)
	if err != nil {
		t.Fatalf("SendToSessionOptsWithAttachments returned error: %v", err)
	}
	if platform != "bridge" {
		t.Fatalf("platform = %q, want bridge", platform)
	}
	if got := bridge.getSent(); len(got) != 1 {
		t.Fatalf("bridge sent = %#v, want one message", got)
	}
}

// TestSendToSessionOpts_PlatformNotFound: unknown platform → sentinel error.
func TestSendToSessionOpts_PlatformNotFound(t *testing.T) {
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{wecom}, "", LangEnglish)

	_, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "hello", nil, nil, SendOptions{Platform: "slack"},
	)
	if !errors.Is(err, ErrSendPlatformNotFound) {
		t.Fatalf("err = %v, want ErrSendPlatformNotFound", err)
	}
}

// TestSendToSessionOpts_PlatformMismatch: key does not belong to the platform.
func TestSendToSessionOpts_PlatformMismatch(t *testing.T) {
	slack := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "slack"}}
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{slack, wecom}, "", LangEnglish)

	_, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "hello", nil, nil, SendOptions{Platform: "slack"},
	)
	if !errors.Is(err, ErrSendPlatformMismatch) {
		t.Fatalf("err = %v, want ErrSendPlatformMismatch", err)
	}
}

// TestSendToSessionOpts_ExplicitReusesBindingCtx: when the key is already
// bound to the requested platform, the live replyCtx is reused (no
// reconstruction) and sideText still updates.
func TestSendToSessionOpts_ExplicitReusesBindingCtx(t *testing.T) {
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{wecom}, "", LangEnglish)
	st := &interactiveState{platform: wecom, replyCtx: "live-ctx"}
	e.interactiveStates["wecom:C1:U1"] = st

	if _, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "hello", nil, nil, SendOptions{Platform: "wecom"},
	); err != nil {
		t.Fatalf("SendToSessionOptsWithAttachments returned error: %v", err)
	}
	if wecom.getReconstructKey() != "" {
		t.Fatalf("ReconstructReplyCtx called with %q, want live ctx reuse", wecom.getReconstructKey())
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sideText != "hello" {
		t.Fatalf("sideText = %q, want hello", st.sideText)
	}
}

// TestSendToSessionOpts_ExplicitDoesNotTouchForeignSideText: delivering to a
// platform other than the binding must not update the bound session's
// sideText (it would corrupt the bound platform's outgoing dedup display).
func TestSendToSessionOpts_ExplicitDoesNotTouchForeignSideText(t *testing.T) {
	bridge := &stubPlatformEngine{n: "bridge"}
	wecom := &stubExplicitPlatform{stubPlatformEngine: stubPlatformEngine{n: "wecom"}}
	e := NewEngine("test", &stubAgent{}, []Platform{bridge, wecom}, "", LangEnglish)
	st := &interactiveState{platform: bridge, replyCtx: "bridge-ctx"}
	e.interactiveStates["wecom:C1:U1"] = st

	if _, err := e.SendToSessionOptsWithAttachments(
		"wecom:C1:U1", "hello", nil, nil, SendOptions{Platform: "wecom"},
	); err != nil {
		t.Fatalf("SendToSessionOptsWithAttachments returned error: %v", err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sideText != "" {
		t.Fatalf("sideText = %q, want empty (foreign platform delivery)", st.sideText)
	}
}
