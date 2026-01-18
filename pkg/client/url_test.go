// pkg/client/url_test.go
package client

import (
	"strings"
	"testing"
)

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

func TestNewFromURL_UnixSocket(t *testing.T) {
	// This tests that NewFromURL correctly parses unix:// URLs
	// grpc.NewClient uses lazy connection, so it won't fail even for nonexistent sockets
	// The actual connection happens on the first RPC call
	client, err := NewFromURL("unix:///nonexistent/socket.sock")
	if err != nil {
		t.Errorf("NewFromURL should succeed with lazy connection, got: %v", err)
	}
	if client != nil {
		client.Close()
	}
}

func TestNewFromURL_GRPCNoTLS(t *testing.T) {
	// Test grpc:// scheme (no TLS)
	client, err := NewFromURL("grpc://localhost:65432")
	if err != nil {
		t.Errorf("NewFromURL should succeed with lazy connection, got: %v", err)
	}
	if client != nil {
		client.Close()
	}
}

func TestNewFromURL_GRPCWithTLS(t *testing.T) {
	// Test grpcs:// scheme (with TLS)
	client, err := NewFromURL("grpcs://localhost:65432")
	if err != nil {
		t.Errorf("NewFromURL should succeed with lazy connection, got: %v", err)
	}
	if client != nil {
		client.Close()
	}
}

func TestNewFromURL_BareHostPort(t *testing.T) {
	// Test bare host:port (defaults to grpc://)
	client, err := NewFromURL("localhost:65432")
	if err != nil {
		t.Errorf("NewFromURL should succeed with lazy connection, got: %v", err)
	}
	if client != nil {
		client.Close()
	}
}

func TestNewFromURL_InvalidScheme(t *testing.T) {
	_, err := NewFromURL("http://localhost:8080")
	if err == nil {
		t.Error("expected error for invalid scheme")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported scheme error, got: %v", err)
	}
}

func TestNewFromURL_EmptyURL(t *testing.T) {
	_, err := NewFromURL("")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}
