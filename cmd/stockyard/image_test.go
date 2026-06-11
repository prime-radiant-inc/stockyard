// cmd/stockyard/image_test.go
package main

import "testing"

func TestDisplayRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"hub library ref trimmed", "docker.io/library/alpine:3.21", "alpine:3.21"},
		{"hub library legacy stockyard ref trimmed", "docker.io/library/stockyard-vm:container", "stockyard-vm:container"},
		{"hub org ref drops bare docker.io", "docker.io/obra/foo:1", "obra/foo:1"},
		{"stockyard.local ref unchanged", "stockyard.local/stockyard-vm:container", "stockyard.local/stockyard-vm:container"},
		{"other registry unchanged", "ghcr.io/obra/stockyard-vm:latest", "ghcr.io/obra/stockyard-vm:latest"},
		{"already short ref unchanged", "alpine:3.21", "alpine:3.21"},
		{"hub library digest ref trimmed", "docker.io/library/alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"bare digest ref unchanged", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"empty ref unchanged", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayRef(tc.in); got != tc.want {
				t.Errorf("displayRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
