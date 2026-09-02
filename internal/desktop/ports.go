package desktop

import "net"

func AllocateLoopbackPort() (net.Listener, error) {
	return net.ListenTCP("tcp4", &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	})
}
