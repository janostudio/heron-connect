package relay

import (
	"context"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateRelay(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "short no truncation",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "exact length",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "ascii truncated with ellipsis",
			input:  "hello world",
			maxLen: 5,
			want:   "hello…",
		},
		{
			name:   "chinese truncated",
			input:  "这是一段很长的中文文本需要截断",
			maxLen: 6,
			want:   "这是一段很长…",
		},
		{
			name:   "emoji truncated",
			input:  "🎉🚀💡✨👋🌍",
			maxLen: 2,
			want:   "🎉🚀…",
		},
		{
			name:   "empty string",
			input:  "",
			maxLen: 5,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRelay(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateRelay(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncateRelay(%q, %d) produced invalid UTF-8: %q", tt.input, tt.maxLen, got)
			}
		})
	}
}

func TestRelayManager_DefaultTimeout(t *testing.T) {
	rm := NewRelayManager("")

	if rm.timeout != relayTimeout {
		t.Fatalf("rm.timeout = %v, want %v", rm.timeout, relayTimeout)
	}
}

func TestRelayManager_RelayContextHonorsConfiguredTimeout(t *testing.T) {
	rm := NewRelayManager("")
	rm.SetTimeout(3 * time.Second)

	ctx, cancel := rm.relayContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected relay context deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 3*time.Second {
		t.Fatalf("time until deadline = %v, want within (0, 3s]", remaining)
	}
}

func TestRelayManager_RelayContextDisablesTimeoutAtZero(t *testing.T) {
	rm := NewRelayManager("")
	rm.SetTimeout(0)

	baseCtx := context.Background()
	ctx, cancel := rm.relayContext(baseCtx)
	defer cancel()

	if ctx != baseCtx {
		t.Fatal("expected relayContext to return the original context when timeout is disabled")
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("expected no deadline when timeout is disabled")
	}
}
