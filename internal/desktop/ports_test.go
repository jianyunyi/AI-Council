package desktop

import (
	"net"
	"testing"
)

func TestPortListenerUsesIPv4LoopbackOnly(t *testing.T) {
	listener, err := AllocateLoopbackPort()
	if err != nil {
		t.Fatalf("AllocateLoopbackPort() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address type = %T, want *net.TCPAddr", listener.Addr())
	}
	if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("listener IP = %s, want 127.0.0.1", addr.IP)
	}
	if addr.Port == 0 {
		t.Error("listener port was not allocated")
	}
}
