package ghtmx

import "testing"

func TestEscapePathSegment(t *testing.T) {
	tests := []struct{ in, want string }{
		{"42", "42"},
		{"a/b", "a%2Fb"},
		{"a?b", "a%3Fb"},
		{"a#b", "a%23b"},
		{"a&b", "a&b"}, // & is legal in a path segment
		{"a b", "a%20b"},
		{"ünïcode", "%C3%BCn%C3%AFcode"},
	}
	for _, tt := range tests {
		if got := EscapePathSegment(tt.in); got != tt.want {
			t.Errorf("EscapePathSegment(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapeQueryValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a b", "a+b"},
		{"a&b", "a%26b"},
		{"a=b", "a%3Db"},
		{"a#b", "a%23b"},
	}
	for _, tt := range tests {
		if got := EscapeQueryValue(tt.in); got != tt.want {
			t.Errorf("EscapeQueryValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEscapePathWildcard(t *testing.T) {
	tests := []struct{ in, want string }{
		{"docs/getting started.md", "docs/getting%20started.md"},
		{"a/b/c", "a/b/c"},
		{"a?/b", "a%3F/b"},
	}
	for _, tt := range tests {
		if got := EscapePathWildcard(tt.in); got != tt.want {
			t.Errorf("EscapePathWildcard(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
