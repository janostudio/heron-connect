package core

import (
	"testing"
	"time"
)

func TestResolveBindingPolicy_IdleOnly(t *testing.T) {
	e := &Engine{name: "test", resetOnIdle: 30 * time.Minute}
	p := e.resolveBindingPolicy("web")
	if p.IdleTimeout != 30*time.Minute {
		t.Fatalf("IdleTimeout = %v, want 30m", p.IdleTimeout)
	}
	if p.MaxAge != 0 {
		t.Fatalf("MaxAge = %v, want 0 (disabled by default)", p.MaxAge)
	}
}

func TestResolveBindingPolicy_PlatformOverride(t *testing.T) {
	e := &Engine{
		name:        "test",
		resetOnIdle: 30 * time.Minute,
		resetOnIdleByPlatform: map[string]time.Duration{
			"web":   0,                 // disabled for web
			"wecom": 10 * time.Minute, // shorter for wecom
		},
	}
	if got := e.resolveBindingPolicy("web").IdleTimeout; got != 0 {
		t.Fatalf("web IdleTimeout = %v, want 0 (override)", got)
	}
	if got := e.resolveBindingPolicy("wecom").IdleTimeout; got != 10*time.Minute {
		t.Fatalf("wecom IdleTimeout = %v, want 10m", got)
	}
	// Unknown platform falls back to engine default.
	if got := e.resolveBindingPolicy("telegram").IdleTimeout; got != 30*time.Minute {
		t.Fatalf("telegram IdleTimeout = %v, want 30m (fallback)", got)
	}
}

func TestSetBindingMaxAge(t *testing.T) {
	e := &Engine{name: "test"}
	if got := e.resolveBindingPolicy("").MaxAge; got != 0 {
		t.Fatalf("default MaxAge = %v, want 0", got)
	}
	e.SetBindingMaxAge(2 * time.Hour)
	if got := e.resolveBindingPolicy("").MaxAge; got != 2*time.Hour {
		t.Fatalf("MaxAge = %v, want 2h", got)
	}
	e.SetBindingMaxAge(-time.Minute)
	if got := e.resolveBindingPolicy("").MaxAge; got != 0 {
		t.Fatalf("negative MaxAge = %v, want 0 (clamped)", got)
	}
}

// TestReapIdleSessions_UsesPlatformPolicy guards the web-idle-misswitch fix:
// the reaper must resolve the idle timeout from the state's LOGICAL platform
// name (platformName), not the engine-global resetOnIdle.
func TestReapIdleSessions_UsesPlatformPolicy(t *testing.T) {
	e := &Engine{
		name:        "test",
		resetOnIdle: 30 * time.Minute,
		resetOnIdleByPlatform: map[string]time.Duration{
			"web": 0, // web must never be idle-reaped
		},
		interactiveStates: map[string]*interactiveState{},
	}

	// A web-identified state whose agent has been "idle" far past the global 30m.
	webState := &interactiveState{
		platformName:   "web",
		agentSession:   newControllableSession("s1"),
		lastEventTime:  time.Now().Add(-2 * time.Hour),
		platform:       &stubPlatformEngine{n: "bridge"},
	}
	webState.agentSession.(*controllableAgentSession).alive = true
	e.interactiveStates["bridge:web:u1:c1"] = webState

	// A feishu-identified state idle far past 30m (should be reaped).
	feishuState := &interactiveState{
		platformName:  "feishu",
		agentSession:  newControllableSession("s2"),
		lastEventTime: time.Now().Add(-2 * time.Hour),
		platform:      &stubPlatformEngine{n: "feishu"},
	}
	feishuState.agentSession.(*controllableAgentSession).alive = true
	e.interactiveStates["feishu:u1:c1"] = feishuState

	e.reapIdleSessions()

	if _, ok := e.interactiveStates["bridge:web:u1:c1"]; !ok {
		t.Fatal("web binding was reaped despite platform override 0 — regression of web idle misswitch")
	}
	if _, ok := e.interactiveStates["feishu:u1:c1"]; ok {
		t.Fatal("feishu binding was NOT reaped despite being idle past threshold")
	}
}
