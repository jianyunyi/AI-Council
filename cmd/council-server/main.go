package main

import (
	"flag"
	"fmt"
	"net"
	"strconv"

	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/zeromicro/go-zero/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dbPath := flag.String("db", ".data/council.db", "SQLite database path")
	token := flag.String("token", "", "REST bearer token (empty disables auth)")
	runnerAddr := flag.String("runner", "", "Runner gRPC address")
	flag.Parse()

	host, port, err := splitListenAddress(*listen)
	if err != nil {
		panic(err)
	}
	db, err := storage.Open(*dbPath)
	if err != nil {
		panic(err)
	}
	api := transport.NewPersistentAPI(db)
	var runnerConn *grpc.ClientConn
	if *runnerAddr != "" {
		runnerConn, err = grpc.Dial(*runnerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			panic(err)
		}
		defer runnerConn.Close()
		api.WithRunnerClient(runnerv1.NewWorkspaceRunnerClient(runnerConn))
	}
	server := transport.NewServerWithAPIAndAuth(rest.RestConf{Host: host, Port: port}, api, *token)
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
