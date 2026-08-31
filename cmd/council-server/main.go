package main

import (
	"flag"
	"fmt"
	"net"
	"strconv"

	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/zeromicro/go-zero/rest"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	flag.Parse()

	host, port, err := splitListenAddress(*listen)
	if err != nil {
		panic(err)
	}
	server := transport.NewServer(rest.RestConf{Host: host, Port: port})
	defer server.Stop()
	fmt.Printf("council-server listening on %s\n", *listen)
	server.Start()
}

func splitListenAddress(address string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("parse listen address: %w", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port %q", rawPort)
	}
	return host, port, nil
}
