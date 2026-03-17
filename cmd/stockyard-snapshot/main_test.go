package main

import (
	"testing"
)

func TestSanitizeLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"edit-main.py", "edit-main.py"},
		{"bash npm test", "bash-npm-test"},
		{"Read /etc/passwd", "Read--etc-passwd"},
		{"", ""},
		{"simple", "simple"},
		{"with_underscore", "with_underscore"},
		{"MixedCase123", "MixedCase123"},
		{"special!@#$%chars", "special-----chars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeLabel(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeLabel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
