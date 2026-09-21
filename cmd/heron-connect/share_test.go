package main

import "testing"

// shareTokenFromArg accepts both a bare token and a full share URL, because the
// thing a user has in hand is the link — making them strip the prefix by hand
// would be busywork. Getting this wrong would silently revoke nothing (or the
// wrong thing), so the parsing is pinned here.
func TestShareTokenFromArg(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare token", "81beefe8dd797ef9544216b4f79d466e", "81beefe8dd797ef9544216b4f79d466e"},
		{"full url", "http://localhost:9820/api/v1/share/abc123", "abc123"},
		{"https url", "https://host.example.com/api/v1/share/abc123", "abc123"},
		{"url with query", "http://h/api/v1/share/abc123?download=1", "abc123"},
		{"url with fragment", "http://h/api/v1/share/abc123#x", "abc123"},
		{"trailing slash", "http://h/api/v1/share/abc123/", "abc123"},
		{"trailing slash and query", "http://h/api/v1/share/abc123/?x=1", "abc123"},
		{"surrounding spaces", "  abc123  ", "abc123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shareTokenFromArg(tc.in); got != tc.want {
				t.Errorf("shareTokenFromArg(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestURLQueryEscape(t *testing.T) {
	// Project names are simple, but escaping the separators that would break
	// out of the query keeps a stray character from truncating the filter.
	tests := map[string]string{
		"demo": "demo",
		"a&b":  "a%26b",
		"a=b":  "a%3Db",
		"a b":  "a%20b",
		"a/b":  "a%2Fb",
		"a?b":  "a%3Fb",
		"a#b":  "a%23b",
		"100%": "100%25",
		"a+b":  "a%2Bb",
	}
	for in, want := range tests {
		if got := urlQueryEscape(in); got != want {
			t.Errorf("urlQueryEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
