package mcp

import (
	"errors"
	"testing"
)

func TestValidateTransportConfigRejectsShellLikeControlData(t *testing.T) {
	if err := validateTransportConfig(TransportStdio, &StdioConfig{Command: "node\x00evil"}, nil); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("stdio NUL error = %v", err)
	}
	if err := validateTransportConfig(TransportStreamableHTTP, nil, &HTTPConfig{Endpoint: "https://example.com/mcp?token=secret"}); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("HTTP query error = %v", err)
	}
	if err := validateTransportConfig(TransportStreamableHTTP, nil, &HTTPConfig{Endpoint: "https://example.com/mcp#token"}); !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("HTTP fragment error = %v", err)
	}
}
