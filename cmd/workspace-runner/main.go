package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	runnergrpc "github.com/aicouncil/aicouncil/internal/runner/grpc"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/runner"
	"google.golang.org/grpc"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8081", "HTTP listen address")
	grpcListen := flag.String("grpc-listen", "127.0.0.1:9091", "gRPC listen address")
	workspaceRoot := flag.String("workspace-root", ".", "workspace root")
	dbPath := flag.String("db", ".data/runner.db", "SQLite database path")
	token := flag.String("token", "", "gRPC bearer token")
	flag.Parse()

	server := &http.Server{
		Addr:              *listen,
		Handler:           transport.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	db, err := storage.Open(*dbPath)
	if err != nil {
		panic(err)
	}
	rpcService, err := runnergrpc.NewServiceWithDB(*workspaceRoot, db)
	if err != nil {
		panic(err)
	}
	grpcServer := grpc.NewServer(runnergrpc.UnaryAuthInterceptor(*token))
	runnerv1.RegisterWorkspaceRunnerServer(grpcServer, rpcService)
	grpcListener, err := net.Listen("tcp", *grpcListen)
	if err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
	}()
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil {
			panic(err)
		}
	}()

	fmt.Printf("workspace-runner listening on %s\n", *listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}
