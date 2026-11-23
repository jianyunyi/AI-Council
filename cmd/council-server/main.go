package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appTask "github.com/aicouncil/aicouncil/internal/app/task"
	councilengine "github.com/aicouncil/aicouncil/internal/council"
	"github.com/aicouncil/aicouncil/internal/observability/metrics"
	"github.com/aicouncil/aicouncil/internal/provider"
	"github.com/aicouncil/aicouncil/internal/provider/anthropic"
	"github.com/aicouncil/aicouncil/internal/provider/deepseek"
	"github.com/aicouncil/aicouncil/internal/provider/openai"
	runnerv1 "github.com/aicouncil/aicouncil/internal/runner/rpc/generated"
	identitysvc "github.com/aicouncil/aicouncil/internal/security/identity"
	providerkeysvc "github.com/aicouncil/aicouncil/internal/security/providerkeys"
	storage "github.com/aicouncil/aicouncil/internal/storage/sqlite"
	transport "github.com/aicouncil/aicouncil/internal/transport/council"
	"github.com/zeromicro/go-zero/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
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
	rbacService, err := configureRBAC(context.Background(), db, auth)
	if err != nil {
		panic(err)
	}
	if err := migrateLegacyOwnership(context.Background(), db, auth.BootstrapSubject); err != nil {
		panic(err)
	}
	api := transport.NewPersistentAPI(db)
	// Pin the artifact root beside the database (same data volume) so verification
	// artifacts survive container recreation and the backup tool can find them.
	// Default: <dir of --db>/artifacts. Override with AICOUNCIL_ARTIFACT_ROOT.
	artifactRoot := os.Getenv("AICOUNCIL_ARTIFACT_ROOT")
	if artifactRoot == "" {
		artifactRoot = filepath.Join(filepath.Dir(*dbPath), "artifacts")
	}
	api = api.WithArtifactRoot(artifactRoot)
	var providerKeyService *providerkeysvc.Service
	if rawKey := os.Getenv("PROVIDER_KEY_ENCRYPTION_KEY"); rawKey != "" {
		masterKey, decodeErr := hex.DecodeString(rawKey)
		if decodeErr != nil || len(masterKey) != 32 {
			panic("PROVIDER_KEY_ENCRYPTION_KEY must be exactly 64 hexadecimal characters")
		}
		keyService, serviceErr := providerkeysvc.New(db, masterKey)
		if serviceErr != nil {
			panic(serviceErr)
		}
		providerKeyService = keyService
		api.WithProviderKeys(keyService)
	}
	if workflow := configuredWorkflow(api.Metrics()); workflow != nil {
		api.WithCouncil(workflow)
	}
	if providerKeyService != nil && rbacService != nil {
		api.WithCouncilFactory(func(ctx context.Context, ownerID string) (appTask.CouncilPort, error) {
			profiles, err := providerKeyService.List(ctx, ownerID)
			if err != nil {
				return nil, err
			}
			credentials := make(map[string]modelCredential)
			for _, profile := range profiles {
				if !profile.HasKey {
					continue
				}
				if _, exists := credentials[profile.Provider]; exists {
					continue
				}
				secret, err := providerKeyService.Secret(ctx, ownerID, profile.ID)
				if err != nil {
					return nil, err
				}
				credentials[profile.Provider] = modelCredential{Key: secret, Model: profile.Model}
			}
			if len(credentials) == 0 {
				return configuredWorkflow(api.Metrics()), nil
			}
			return configuredWorkflowFor(credentials, api.Metrics()), nil
		})
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
		api.WithReadinessCheck(func(context.Context) error {
			runnerConn.Connect()
			state := runnerConn.GetState()
			if state != connectivity.Ready {
				return fmt.Errorf("runner gRPC connection is %s", state)
			}
			return nil
		})
		if err := migrateRunnerWorkspaces(context.Background(), db, runnerv1.NewWorkspaceRunnerClient(runnerConn), envOr("RUNNER_WORKSPACE_ROOT", "/workspace")); err != nil {
			panic(err)
		}
	}
	var server *rest.Server
	if (*tlsCert == "") != (*tlsKey == "") {
		panic("tls-cert and tls-key must be provided together")
	}
	identityService := identitysvc.New(db)
	mailer, err := configuredMailer(os.Getenv)
	if err != nil {
		panic(err)
	}
	oauthService, err := configuredOAuth(db, os.Getenv)
	if err != nil {
		panic(err)
	}
	if *tlsCert != "" && rbacService != nil {
		server, err = transport.NewTLSServerWithAPIAndAuthFlows(rest.RestConf{Host: host, Port: port}, api, rbacService, auth.Role, *tlsCert, *tlsKey, auth.Session, identityService, mailer, oauthService)
		if err != nil {
			panic(err)
		}
	} else if *tlsCert != "" {
		server, err = transport.NewTLSServerWithAPIAndAuth(rest.RestConf{Host: host, Port: port}, api, *token, *tlsCert, *tlsKey)
		if err != nil {
			panic(err)
		}
	} else if rbacService != nil {
		server = transport.NewServerWithAPIAndAuthFlows(rest.RestConf{Host: host, Port: port}, api, rbacService, auth.Role, auth.Session, identityService, mailer, oauthService)
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

type workspaceRegistrationClient interface {
	RegisterWorkspace(context.Context, *runnerv1.RegisterWorkspaceRequest, ...grpc.CallOption) (*runnerv1.RegisterWorkspaceResponse, error)
}

func migrateRunnerWorkspaces(ctx context.Context, db *gorm.DB, runner workspaceRegistrationClient, runnerRoot string) error {
	var records []storage.WorkspaceRecord
	if err := db.WithContext(ctx).Order("id").Find(&records).Error; err != nil {
		return err
	}
	root, err := filepath.Abs(runnerRoot)
	if err != nil {
		return err
	}
	type migration struct {
		record   storage.WorkspaceRecord
		relative string
	}
	migrations := make([]migration, 0, len(records))
	for _, record := range records {
		relative := record.RunnerPath
		if relative == "" {
			relative, err = filepath.Rel(root, record.CanonicalRoot)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				return fmt.Errorf("legacy workspace %q is outside Runner root; move it under %s and update its record", record.ID, root)
			}
		}
		clean := filepath.Clean(relative)
		if strings.TrimSpace(relative) == "" || strings.HasPrefix(relative, "/") || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || strings.Contains(relative, `\`) || strings.Contains(relative, ":") || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("workspace %q has an unsafe Runner path", record.ID)
		}
		migrations = append(migrations, migration{record: record, relative: relative})
	}
	for _, item := range migrations {
		record, relative := item.record, item.relative
		registered, registerErr := runner.RegisterWorkspace(ctx, &runnerv1.RegisterWorkspaceRequest{WorkspaceId: record.ID, RelativePath: relative})
		if registerErr != nil {
			return fmt.Errorf("register workspace %q with Runner: %w", record.ID, registerErr)
		}
		if registered == nil {
			return fmt.Errorf("register workspace %q with Runner returned no response", record.ID)
		}
		if err := db.WithContext(ctx).Model(&storage.WorkspaceRecord{}).Where("id = ?", record.ID).Updates(map[string]any{
			"runner_path": relative, "canonical_root": registered.Root, "is_git": registered.IsGit, "dirty": registered.Dirty,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func configuredMailer(getenv func(string) string) (transport.Mailer, error) {
	host, port, from := getenv("SMTP_HOST"), getenv("SMTP_PORT"), getenv("SMTP_FROM")
	username, password := getenv("SMTP_USERNAME"), getenv("SMTP_PASSWORD")
	if host == "" && port == "" && from == "" && username == "" && password == "" {
		return nil, nil
	}
	if host == "" || port == "" || from == "" || ((username == "") != (password == "")) {
		return nil, fmt.Errorf("SMTP_HOST, SMTP_PORT and SMTP_FROM are required together; SMTP_USERNAME and SMTP_PASSWORD must be paired")
	}
	return transport.SMTPMailer{Host: host, Port: port, From: from, Username: username, Password: password}, nil
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

func configuredWorkflow(collector *metrics.Metrics) *councilengine.Workflow {
	credentials := make(map[string]modelCredential)
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		credentials["openai"] = modelCredential{Key: key, Model: envOr("OPENAI_MODEL", "gpt-4o")}
	}
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		credentials["deepseek"] = modelCredential{Key: key, Model: envOr("DEEPSEEK_MODEL", "deepseek-chat")}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		credentials["anthropic"] = modelCredential{Key: key, Model: envOr("ANTHROPIC_MODEL", "claude-3-5-sonnet")}
	}
	return configuredWorkflowFor(credentials, collector)
}

type modelCredential struct {
	Key   string
	Model string
}

func configuredWorkflowFor(credentials map[string]modelCredential, collector *metrics.Metrics) *councilengine.Workflow {
	items := make([]provider.ModelProvider, 0, len(credentials))
	seats := make([]councilengine.Seat, 0, len(credentials))
	for _, name := range []string{"openai", "deepseek", "anthropic"} {
		credential, ok := credentials[name]
		if !ok || credential.Key == "" || credential.Model == "" {
			continue
		}
		model := credential.Model
		switch name {
		case "openai":
			items = append(items, provider.WithCostMeter(openai.New(openai.Config{APIKey: credential.Key, Model: model}), collector))
		case "deepseek":
			items = append(items, provider.WithCostMeter(deepseek.New(deepseek.Config{APIKey: credential.Key, Model: model}), collector))
		case "anthropic":
			items = append(items, provider.WithCostMeter(anthropic.New(anthropic.Config{APIKey: credential.Key, Model: model}), collector))
		}
		seats = append(seats, councilengine.Seat{ID: name, Provider: name, Model: model, Role: "proposer"})
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
