package main

import "testing"

func TestSplitListenAddress(t *testing.T) {
	host, port, err := splitListenAddress("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("splitListenAddress() error = %v", err)
	}
	if host != "127.0.0.1" || port != 8080 {
		t.Fatalf("splitListenAddress() = %q, %d, want 127.0.0.1, 8080", host, port)
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
