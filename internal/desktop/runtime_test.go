package desktop

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStarter struct {
	mu        sync.Mutex
	specs     []ProcessSpec
	processes []*fakeProcess
}

func (s *fakeStarter) Start(_ context.Context, spec ProcessSpec) (Process, error) {
	p := newFakeProcess()
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.processes = append(s.processes, p)
	s.mu.Unlock()
	return p, nil
}

func (s *fakeStarter) snapshot() ([]ProcessSpec, []*fakeProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ProcessSpec(nil), s.specs...), append([]*fakeProcess(nil), s.processes...)
}

type fakeProcess struct {
	wait       chan error
	stopOnce   sync.Once
	killOnce   sync.Once
	stopped    chan struct{}
	killed     chan struct{}
	ignoreStop bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{wait: make(chan error, 1), stopped: make(chan struct{}), killed: make(chan struct{})}
}

func (p *fakeProcess) Wait() error { return <-p.wait }

func (p *fakeProcess) Stop() error {
	p.stopOnce.Do(func() { close(p.stopped) })
	if !p.ignoreStop {
		p.wait <- nil
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.killOnce.Do(func() {
		close(p.killed)
		select {
		case p.wait <- errors.New("killed"):
		default:
		}
	})
	return nil
}

func TestRuntimeStartsLoopbackServicesWithIsolatedConfiguration(t *testing.T) {
	dataDir := t.TempDir()
	starter := &fakeStarter{}
	runtime := NewRuntime(RuntimeOptions{
		Config:        Config{DataDir: dataDir, LogDir: filepath.Join(dataDir, "logs"), CouncilBinary: "council", RunnerBinary: "runner"},
		WorkspaceRoot: "C:/workspace",
		Starter:       starter,
		HealthCheck:   func(context.Context, string) error { return nil },
		TLSCert:       "desktop.crt",
		TLSKey:        "desktop.key",
		RBACRole:      "desktop-user",
		RBACSubject:   "local-user",
		Environment:   map[string]string{"OPENAI_API_KEY": "provider-secret"},
	})

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	specs, _ := starter.snapshot()
	if len(specs) != 2 {
		t.Fatalf("started %d processes, want 2", len(specs))
	}
	runner, council := specs[0], specs[1]
	if runner.Name != "runner" || council.Name != "council" {
		t.Fatalf("process order = %q, %q, want runner then council", runner.Name, council.Name)
	}
	if runner.Stdout == nil || runner.Stderr == nil || council.Stdout == nil || council.Stderr == nil {
		t.Fatal("child process output is not routed to bounded logs")
	}

	runnerHTTP := flagValue(t, runner.Args, "--listen")
	runnerGRPC := flagValue(t, runner.Args, "--grpc-listen")
	councilHTTP := flagValue(t, council.Args, "--listen")
	for _, address := range []string{runnerHTTP, runnerGRPC, councilHTTP} {
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			t.Errorf("address %q is not IPv4 loopback", address)
		}
	}

	token := flagValue(t, runner.Args, "--token")
	if token == "" || token != flagValue(t, council.Args, "--token") {
		t.Fatal("runner and council did not receive the same random session token")
	}
	if token == "provider-secret" {
		t.Fatal("session token reused a provider secret")
	}
	if got := flagValue(t, runner.Args, "--db"); got != filepath.Join(dataDir, "runner.db") {
		t.Errorf("runner db = %q", got)
	}
	if got := flagValue(t, council.Args, "--db"); got != filepath.Join(dataDir, "council.db") {
		t.Errorf("council db = %q", got)
	}
	if got := flagValue(t, council.Args, "--runner"); got != runnerGRPC {
		t.Errorf("council runner address = %q, want %q", got, runnerGRPC)
	}
	if got := flagValue(t, council.Args, "--runner-token"); got != token {
		t.Errorf("council runner token = %q, want shared session token", got)
	}
	if !hasFlag(council.Args, "--runner-tls") {
		t.Errorf("council TLS runner flag missing from %v", council.Args)
	}
	for _, args := range [][]string{runner.Args, council.Args} {
		if flagValue(t, args, "--tls-cert") != "desktop.crt" || flagValue(t, args, "--tls-key") != "desktop.key" {
			t.Errorf("TLS flags missing from %v", args)
		}
	}
	if flagValue(t, council.Args, "--rbac-role") != "desktop-user" || flagValue(t, council.Args, "--rbac-bootstrap-subject") != "local-user" || flagValue(t, council.Args, "--rbac-bootstrap-token") != token {
		t.Errorf("RBAC flags missing from %v", council.Args)
	}
	if got := council.Env["OPENAI_API_KEY"]; got != "provider-secret" {
		t.Errorf("council environment secret = %q", got)
	}
	if _, ok := runner.Env["OPENAI_API_KEY"]; ok {
		t.Error("provider key must not be injected into runner")
	}
	if strings.Contains(strings.Join(runner.Args, " ")+strings.Join(council.Args, " "), "provider-secret") {
		t.Fatal("provider secret leaked into process arguments")
	}
}

func TestRuntimeWaitReadyChecksBothServices(t *testing.T) {
	starter := &fakeStarter{}
	var mu sync.Mutex
	checks := map[string]int{}
	runtime := NewRuntime(RuntimeOptions{
		Config:       Config{DataDir: t.TempDir(), LogDir: t.TempDir(), CouncilBinary: "council", RunnerBinary: "runner"},
		Starter:      starter,
		PollInterval: time.Millisecond,
		HealthCheck: func(_ context.Context, url string) error {
			mu.Lock()
			defer mu.Unlock()
			checks[url]++
			if checks[url] < 2 {
				return errors.New("not ready")
			}
			return nil
		},
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	status := runtime.Status()
	if status.State != StateReady {
		t.Fatalf("status state = %q, want %q", status.State, StateReady)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(checks) != 2 {
		t.Fatalf("health checks = %v, want both services", checks)
	}
}

func TestRuntimeStopForceKillsAfterTimeout(t *testing.T) {
	starter := &fakeStarter{}
	runtime := NewRuntime(RuntimeOptions{
		Config:      Config{DataDir: t.TempDir(), LogDir: t.TempDir(), CouncilBinary: "council", RunnerBinary: "runner"},
		Starter:     starter,
		HealthCheck: func(context.Context, string) error { return nil },
		StopTimeout: 5 * time.Millisecond,
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	_, processes := starter.snapshot()
	for _, process := range processes {
		process.ignoreStop = true
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	for _, process := range processes {
		select {
		case <-process.stopped:
		default:
			t.Error("process did not receive graceful stop")
		}
		select {
		case <-process.killed:
		default:
			t.Error("process was not force-killed after timeout")
		}
	}
	if got := runtime.Status().State; got != StateStopped {
		t.Fatalf("status state = %q, want %q", got, StateStopped)
	}
}

func TestRuntimeReportsSanitizedErrorWhenServiceExits(t *testing.T) {
	starter := &fakeStarter{}
	runtime := NewRuntime(RuntimeOptions{
		Config:      Config{DataDir: t.TempDir(), LogDir: t.TempDir(), CouncilBinary: "council", RunnerBinary: "runner"},
		Starter:     starter,
		HealthCheck: func(context.Context, string) error { return nil },
	})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, processes := starter.snapshot()
	processes[0].wait <- nil

	deadline := time.Now().Add(time.Second)
	for runtime.Status().State != StateFailed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := runtime.Status()
	if status.State != StateFailed {
		t.Fatalf("status state = %q, want %q", status.State, StateFailed)
	}
	if status.LastError != "runner exited unexpectedly" {
		t.Fatalf("status error = %q, want sanitized service error", status.LastError)
	}
}

func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	t.Fatalf("flag %s missing from %v", flag, args)
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, value := range args {
		if value == flag {
			return true
		}
	}
	return false
}
