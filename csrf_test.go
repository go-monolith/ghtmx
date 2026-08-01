package ghtmx

import "testing"

func TestCSRFHeader(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		header []string
		want   string
	}{
		{"default header", "tok123", nil, `{"X-CSRF-Token":"tok123"}`},
		{"custom header", "tok123", []string{"X-XSRF-TOKEN"}, `{"X-XSRF-TOKEN":"tok123"}`},
		{"empty custom falls back", "tok123", []string{""}, `{"X-CSRF-Token":"tok123"}`},
		{"token is escaped", `a"b\c`, nil, `{"X-CSRF-Token":"a\"b\\c"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CSRFHeader(tt.token, tt.header...); got != tt.want {
				t.Errorf("CSRFHeader = %q, want %q", got, tt.want)
			}
		})
	}
}
