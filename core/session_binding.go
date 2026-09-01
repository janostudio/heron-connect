package core

// session_binding.go — session binding lifecycle policy.
//
// Introduces an explicit policy object for "IM session ↔ agent session"
// binding reclamation, generalizing the existing resetOnIdle behavior (an
// agent-event-idle timeout) with an additional hard MaxAge bound. This is a
// step toward openclaw's thread-bindings-policy without changing the existing
// resetOnIdle semantics: IdleTimeout maps 1:1 to resetOnIdle, MaxAge is an
// additive, opt-in bound (0 = disabled, matching current behavior).

import (
	"strings"
	"time"
)

// SessionBindingPolicy governs when a binding is reclaimed.
type SessionBindingPolicy struct {
	// IdleTimeout is how long a binding may go without agent events before
	// reclamation. <=0 disables idle reaping. Maps to the existing resetOnIdle.
	IdleTimeout time.Duration
	// MaxAge is a hard bound on a binding's total lifetime (from spawn), even
	// if it is actively used. <=0 disables the hard bound.
	MaxAge time.Duration
}

// resolveBindingPolicy returns the effective binding policy for a platform
// name, applying the same per-platform override precedence as resetOnIdle
// (platform override > engine default). MaxAge is engine-global for now; a
// per-platform MaxAge can be layered in later without breaking this API.
func (e *Engine) resolveBindingPolicy(platformName string) SessionBindingPolicy {
	return SessionBindingPolicy{
		IdleTimeout: e.resolveResetOnIdleForPlatform(platformName),
		MaxAge:      e.bindingMaxAge,
	}
}

// SetBindingMaxAge sets the engine-wide hard cap on a session binding's total
// lifetime. 0 disables (default). Negative values are treated as 0.
func (e *Engine) SetBindingMaxAge(d time.Duration) {
	if d <= 0 {
		e.bindingMaxAge = 0
		return
	}
	e.bindingMaxAge = d
}

// normalizePlatformKey lowercases/trims a platform name for override lookup.
func normalizePlatformKey(platformName string) string {
	return strings.ToLower(strings.TrimSpace(platformName))
}
