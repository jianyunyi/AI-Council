package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	councilengine "github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/anthropic"
	"github.com/aicouncil/aicouncil/internal/provider/deepseek"
	"github.com/aicouncil/aicouncil/internal/provider/openai"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/zeromicro/go-zero/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dbPath := flag.String("db", ".data/council.db", "SQLite database path")
	token := flag.String("token", "", "REST bearer token (empty disables auth)")
	tlsCert := flag.String("tls-cert", "", "REST TLS certificate file (hot reloaded)")
	tlsKey := flag.String("tls-key", "", "REST TLS private key file (hot reloaded)")
	auth := registerAuthFlags(flag.CommandLine, os.Getenv)
	runnerAddr := flag.String("runner", "", "Runner gRPC address")
	runnerTLS := flag.Bool("runner-tls", false, "Use TLS for Runner gRPC")
	runnerTLSServerName := flag.String("runner-tls-server-name", "", "TLS server name for Runner gRPC")
	runnerToken := flag.String("runner-token", "", "Runner gRPC bearer token")
	flag.Parse()
	if err := auth.validate(); err != nil {
		panic(err)
	}

	host, port, err := splitListenAddress(*listen)
	if err != nil {
		panic(err)
	}
	db, err := storage.Open(*dbPath)
	if err != nil {
		panic(err)
	}
	api := transport.NewPersistentAPI(db)
	if workflow := configuredWorkflow(); workflow != nil {
		api.WithCouncil(workflow)
	}
	var runnerConn *grpc.ClientConn
	if *runnerAddr != "" {
		var creds credentials.TransportCredentials = insecure.NewCredentials()
		if *runnerTLS {
			creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, ServerName: *runnerTLSServerName})
		}
		runnerConn, err = grpc.Dial(*runnerAddr, runnerDialOptions(creds, *runnerToken)...)
		if err != nil {
			panic(err)
		}
		defer runnerConn.Close()
		api.WithRunnerClient(runnerv1.NewWorkspaceRunnerClient(runnerConn))
	}
	var server *rest.Server
	if (*tlsCert == "") != (*tlsKey == "") {
		panic("tls-cert and tls-key must be provided together")
	}
	rbacService, err := configureRBAC(context.Background(), db, auth)
	if err != nil {
		panic(err)
	}
	if *tlsCert != "" && rbacService != nil {
		server, err = transport.NewTLSServerWithAPIAndRBACWithOptions(rest.RestConf{Host: host, Port: port}, api, rbacService, auth.Role, *tlsCert, *tlsKey, auth.Session)
		if err != nil {
			panic(err)
		}
	} else if *tlsCert != "" {
		server, err = transport.NewTLSServerWithAPIAndAuth(rest.RestConf{Host: host, Port: port}, api, *token, *tlsCert, *tlsKey)
		if err != nil {
			panic(err)
		}
	} else if rbacService != nil {
		server = transport.NewServerWithAPIAndRBACWithOptions(rest.RestConf{Host: host, Port: port}, api, rbacService, auth.Role, auth.Session)
	} else {
		server = transport.NewServerWithAPIAndAuth(rest.RestConf{Host: host, Port: port}, api, *token)
	}
	var stopOnce sync.Once
	server.AddRoute(rest.Route{
		Method:  http.MethodPost,
		Path:    "/shutdown",
		Handler: transport.ShutdownHandler(func() { stopOnce.Do(server.Stop) }),
	})
	defer stopOnce.Do(server.Stop)
	fmt.Printf("council-server listening on %s\n", *listen)
	server.Start()
}

func runnerDialOptions(creds credentials.TransportCredentials, token string) []grpc.DialOption {
	options := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if token == "" {
		return options
	}
	return append(options, grpc.WithUnaryInterceptor(runnerAuthInterceptor(token)))
}

func runnerAuthInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOptions ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, callOptions...)
	}
}

func configuredWorkflow() *councilengine.Workflow {
	items := make([]provider.ModelProvider, 0, 3)
	seats := make([]councilengine.Seat, 0, 3)
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		model := envOr("OPENAI_MODEL", "gpt-4o")
		items = append(items, provider.WithCostMeter(openai.New(openai.Config{APIKey: key, Model: model})))
		seats = append(seats, councilengine.Seat{ID: "openai", Provider: "openai", Model: model, Role: "proposer"})
	}
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		model := envOr("DEEPSEEK_MODEL", "deepseek-chat")
		items = append(items, provider.WithCostMeter(deepseek.New(deepseek.Config{APIKey: key, Model: model})))
		seats = append(seats, councilengine.Seat{ID: "deepseek", Provider: "deepseek", Model: model, Role: "proposer"})
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		model := envOr("ANTHROPIC_MODEL", "claude-3-5-sonnet")
		items = append(items, provider.WithCostMeter(anthropic.New(anthropic.Config{APIKey: key, Model: model})))
		seats = append(seats, councilengine.Seat{ID: "anthropic", Provider: "anthropic", Model: model, Role: "proposer"})
	}
	if len(items) == 0 {
		return nil
	}
	registry := provider.NewRegistry(items...)
	engine := councilengine.NewEngine(registry, nil, councilengine.Limits{Timeout: 2 * time.Minute})
	// Use separate role identities even when a deployment configures one vendor.
	proposers := append([]councilengine.Seat(nil), seats...)
	reviewers := append([]councilengine.Seat(nil), seats...)
	judge := seats[0]
	judge.ID += "-judge"
	judge.Role = "judge"
	redTeam := seats[0]
	redTeam.ID += "-redteam"
	redTeam.Role = "redteam"
	return councilengine.NewWorkflow(engine, proposers, reviewers, judge, redTeam)
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
