package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultPollInterval = 100 * time.Millisecond
	defaultStopTimeout  = 5 * time.Second
)

type RuntimeState string

const (
	StateStopped  RuntimeState = "stopped"
	StateStarting RuntimeState = "starting"
	StateReady    RuntimeState = "ready"
	StateStopping RuntimeState = "stopping"
	StateFailed   RuntimeState = "failed"
)

type RuntimeStatus struct {
	State             RuntimeState
	CouncilURL        string
	RunnerURL         string
	RunnerGRPCAddress string
	LastError         string
}

// HealthChecker probes a child service readiness URL.
type HealthChecker interface {
	Check(context.Context, string) error
}

type HealthCheckFunc func(context.Context, string) error

func (check HealthCheckFunc) Check(ctx context.Context, url string) error {
	return check(ctx, url)
}

type RuntimeOptions struct {
	Config        Config
	WorkspaceRoot string
	Starter       CommandStarter
	HealthChecker HealthChecker
	HealthCheck   HealthCheckFunc
	PollInterval  time.Duration
	StopTimeout   time.Duration
	TLSCert       string
	TLSKey        string
	RBACRole      string
	RBACSubject   string
	Environment   map[string]string
}

type Runtime struct {
	mu     sync.Mutex
	stopMu sync.Mutex

	options   RuntimeOptions
	starter   CommandStarter
	checker   HealthChecker
	status    RuntimeStatus
	children  []*managedProcess
	stopWatch chan struct{}
}

type managedProcess struct {
	name    string
	process Process
	done    chan struct{}
}

func NewRuntime(options RuntimeOptions) *Runtime {
	starter := options.Starter
	if starter == nil {
		starter = ExecCommandStarter{}
	}
	checker := options.HealthChecker
	if checker == nil && options.HealthCheck != nil {
		checker = options.HealthCheck
	}
	if checker == nil {
		checker = HealthCheckFunc(defaultHealthCheck)
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.StopTimeout <= 0 {
		options.StopTimeout = defaultStopTimeout
	}
	return &Runtime{
		options: options,
		starter: starter,
		checker: checker,
		status:  RuntimeStatus{State: StateStopped},
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.status.State != StateStopped {
		state := r.status.State
		r.mu.Unlock()
		return fmt.Errorf("desktop runtime cannot start while %s", state)
	}
	r.status = RuntimeStatus{State: StateStarting}
	r.stopWatch = make(chan struct{})
	r.mu.Unlock()

	runnerHTTP, err := reserveLoopbackAddress()
	if err != nil {
		return r.failStart("allocate runner HTTP address")
	}
	runnerGRPC, err := reserveLoopbackAddress()
	if err != nil {
		return r.failStart("allocate runner gRPC address")
	}
	councilHTTP, err := reserveLoopbackAddress()
	if err != nil {
		return r.failStart("allocate council HTTP address")
	}
	token, err := NewSessionToken()
	if err != nil {
		return r.failStart("create session token")
	}

	runnerArgs := []string{
		"--listen", runnerHTTP,
		"--grpc-listen", runnerGRPC,
		"--workspace-root", r.options.WorkspaceRoot,
		"--db", filepath.Join(r.options.Config.DataDir, "runner.db"),
		"--token", token,
		"--tls-cert", r.options.TLSCert,
		"--tls-key", r.options.TLSKey,
	}
	councilArgs := []string{
		"--listen", councilHTTP,
		"--db", filepath.Join(r.options.Config.DataDir, "council.db"),
		"--token", token,
		"--runner", runnerGRPC,
		"--tls-cert", r.options.TLSCert,
		"--tls-key", r.options.TLSKey,
		"--rbac-role", r.options.RBACRole,
		"--rbac-bootstrap-subject", r.options.RBACSubject,
	}

	startContext := context.WithoutCancel(ctx)
	runner, err := r.startChild(startContext, "runner", ProcessSpec{
		Name: r.options.Config.RunnerBinary,
		Args: runnerArgs,
		Env:  cloneEnvironment(r.options.Environment),
	})
	if err != nil {
		return r.failStart("start runner")
	}
	council, err := r.startChild(startContext, "council", ProcessSpec{
		Name: r.options.Config.CouncilBinary,
		Args: councilArgs,
	})
	if err != nil {
		r.setChildren([]*managedProcess{runner})
		_ = r.Stop(context.Background())
		r.mu.Lock()
		r.status.State = StateFailed
		r.status.LastError = "start council"
		r.mu.Unlock()
		return errors.New("start council")
	}

	r.mu.Lock()
	r.children = []*managedProcess{runner, council}
	r.status.CouncilURL = serviceURL(councilHTTP, r.options.TLSCert, r.options.TLSKey)
	r.status.RunnerURL = "http://" + runnerHTTP
	r.status.RunnerGRPCAddress = runnerGRPC
	stopWatch := r.stopWatch
	r.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			_ = r.Stop(context.Background())
		case <-stopWatch:
		}
	}()
	return nil
}

func (r *Runtime) WaitReady(ctx context.Context) error {
	for {
		status := r.Status()
		switch status.State {
		case StateStopped:
			return errors.New("desktop runtime is not running")
		case StateStopping:
			return errors.New("desktop runtime is stopping")
		case StateFailed:
			if status.LastError == "" {
				return errors.New("desktop runtime failed")
			}
			return errors.New(status.LastError)
		case StateReady:
			return nil
		}

		runnerErr := r.checker.Check(ctx, status.RunnerURL+"/healthz")
		councilErr := r.checker.Check(ctx, status.CouncilURL+"/healthz")
		if runnerErr == nil && councilErr == nil {
			r.mu.Lock()
			if r.status.State == StateStarting {
				r.status.State = StateReady
			}
			ready := r.status.State == StateReady
			r.mu.Unlock()
			if ready {
				return nil
			}
			continue
		}

		timer := time.NewTimer(r.options.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Runtime) Stop(ctx context.Context) error {
	r.stopMu.Lock()
	defer r.stopMu.Unlock()

	r.mu.Lock()
	if r.status.State == StateStopped {
		r.mu.Unlock()
		return nil
	}
	r.status.State = StateStopping
	children := append([]*managedProcess(nil), r.children...)
	if r.stopWatch != nil {
		close(r.stopWatch)
		r.stopWatch = nil
	}
	r.mu.Unlock()

	for i := len(children) - 1; i >= 0; i-- {
		_ = children[i].process.Stop()
	}

	timer := time.NewTimer(r.options.StopTimeout)
	defer timer.Stop()
	for _, child := range children {
		select {
		case <-child.done:
		case <-ctx.Done():
			r.killRunning(children)
			r.markStopped()
			return ctx.Err()
		case <-timer.C:
			r.killRunning(children)
			r.markStopped()
			return nil
		}
	}

	r.markStopped()
	return nil
}

func (r *Runtime) startChild(ctx context.Context, logicalName string, spec ProcessSpec) (*managedProcess, error) {
	process, err := r.starter.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	child := &managedProcess{name: logicalName, process: process, done: make(chan struct{})}
	go func() {
		_ = process.Wait()
		close(child.done)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.status.State == StateStarting || r.status.State == StateReady {
			r.status.State = StateFailed
			r.status.LastError = logicalName + " exited unexpectedly"
		}
	}()
	return child, nil
}

func (r *Runtime) failStart(message string) error {
	r.mu.Lock()
	r.status.State = StateFailed
	r.status.LastError = message
	r.mu.Unlock()
	return errors.New(message)
}

func (r *Runtime) setChildren(children []*managedProcess) {
	r.mu.Lock()
	r.children = children
	r.mu.Unlock()
}

func (r *Runtime) killRunning(children []*managedProcess) {
	for _, child := range children {
		select {
		case <-child.done:
		default:
			_ = child.process.Kill()
		}
	}
}

func (r *Runtime) markStopped() {
	r.mu.Lock()
	r.children = nil
	r.status.State = StateStopped
	r.status.LastError = ""
	r.mu.Unlock()
}

func reserveLoopbackAddress() (string, error) {
	listener, err := AllocateLoopbackPort()
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}
	return address, nil
}

func serviceURL(address, cert, key string) string {
	if cert != "" && key != "" {
		return "https://" + address
	}
	return "http://" + address
}

func cloneEnvironment(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	clone := make(map[string]string, len(environment))
	for key, value := range environment {
		clone[key] = value
	}
	return clone
}

func defaultHealthCheck(ctx context.Context, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
