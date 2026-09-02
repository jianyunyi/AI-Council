package main

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestSplitListenAddress(t *testing.T) {
	host, port, err := splitListenAddress("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("splitListenAddress() error = %v", err)
	}
	if host != "127.0.0.1" || port != 8080 {
		t.Fatalf("splitListenAddress() = %q, %d, want 127.0.0.1, 8080", host, port)
	}
}

func TestRunnerDialOptionsAttachBearerToken(t *testing.T) {
	interceptor := runnerAuthInterceptor("desktop-token")
	err := interceptor(context.Background(), "/runner.Execute", nil, nil, nil, func(ctx context.Context, _ string, _ any, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		values, ok := metadata.FromOutgoingContext(ctx)
		if !ok || len(values.Get("authorization")) != 1 || values.Get("authorization")[0] != "Bearer desktop-token" {
			t.Fatalf("runner authorization metadata = %v", values.Get("authorization"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("runnerAuthInterceptor() error = %v", err)
	}
}

func TestSplitListenAddressRejectsInvalidPort(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:not-a-port"} {
		t.Run(address, func(t *testing.T) {
			if _, _, err := splitListenAddress(address); err == nil {
				t.Fatalf("splitListenAddress(%q) error = nil, want error", address)
			}
		})
	}
}
