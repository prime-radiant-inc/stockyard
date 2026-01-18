// pkg/client/url_test.go
package client

import "testing"

func TestParseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantAddr string
		wantTLS  bool
		wantErr  bool
	}{
		{"unix socket", "unix:///var/run/stockyard.sock", "unix:///var/run/stockyard.sock", false, false},
		{"grpc no TLS", "grpc://localhost:65432", "localhost:65432", false, false},
		{"grpcs with TLS", "grpcs://example.com:65432", "example.com:65432", true, false},
		{"bare host:port defaults to grpc", "myhost:65432", "myhost:65432", false, false},
		{"empty string", "", "", false, true},
		{"invalid scheme", "http://localhost:65432", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, tls, err := ParseURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if addr != tt.wantAddr {
				t.Errorf("ParseURL(%q) addr = %q, want %q", tt.input, addr, tt.wantAddr)
			}
			if tls != tt.wantTLS {
				t.Errorf("ParseURL(%q) tls = %v, want %v", tt.input, tls, tt.wantTLS)
			}
		})
	}
}
