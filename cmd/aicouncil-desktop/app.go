package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aicouncil/aicouncil/internal/desktop"
)

const (
	desktopStartupTimeout = 30 * time.Second
	desktopVersion        = "0.1.0"
)

type DesktopStatus struct {
	State      desktop.RuntimeState `json:"state"`
	Workspace  string               `json:"workspace"`
	CouncilURL string               `json:"council_url"`
	LastError  string               `json:"last_error,omitempty"`
}

type DesktopApp struct {
	mu        sync.Mutex
	config    desktop.Config
	store     *desktop.SecretStore
	workspace string
	runtime   *desktop.Runtime
	lastError string
	wailsCtx  context.Context
}

func (a *DesktopApp) setWailsContext(ctx context.Context) {
	a.mu.Lock()
	a.wailsCtx = ctx
	a.mu.Unlock()
}

func (a *DesktopApp) wailsContext() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wailsCtx
}

func NewDesktopApp(config desktop.Config, store *desktop.SecretStore, workspace string) (*DesktopApp, error) {
	if strings.TrimSpace(config.DataDir) == "" || store == nil {
		return nil, errors.New("desktop configuration and secret store are required")
	}
	if strings.TrimSpace(workspace) == "" {
		workspace = config.DataDir
	}
	return &DesktopApp{config: config, store: store, workspace: workspace}, nil
}

func (a *DesktopApp) Start() (DesktopStatus, error) {
	a.mu.Lock()
	if a.runtime != nil {
		status := a.statusLocked()
		a.mu.Unlock()
		return status, nil
	}
	workspace := a.workspace
	a.mu.Unlock()

	environment, err := a.providerEnvironment()
	if err != nil {
		return a.setStartError(err)
	}
	runtime := desktop.NewRuntime(desktop.RuntimeOptions{
		Config:        a.config,
		WorkspaceRoot: workspace,
		Environment:   environment,
	})
	ctx, cancel := context.WithTimeout(context.Background(), desktopStartupTimeout)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		return a.setStartError(err)
	}
	if err := runtime.WaitReady(ctx); err != nil {
		_ = runtime.Stop(context.Background())
		return a.setStartError(err)
	}

	a.mu.Lock()
	a.runtime = runtime
	a.lastError = ""
	status := a.statusLocked()
	a.mu.Unlock()
	return status, nil
}

func (a *DesktopApp) Stop() error {
	a.mu.Lock()
	runtime := a.runtime
	a.mu.Unlock()
	if runtime == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runtime.Stop(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	if a.runtime == runtime {
		a.runtime = nil
	}
	a.mu.Unlock()
	return nil
}

func (a *DesktopApp) Status() DesktopStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked()
}

// WebRuntime returns the short-lived local API session used by the embedded
// WebView. Provider keys are never part of this response.
func (a *DesktopApp) WebRuntime() (desktop.WebRuntime, error) {
	a.mu.Lock()
	runtime := a.runtime
	a.mu.Unlock()
	if runtime == nil {
		return desktop.WebRuntime{}, errors.New("desktop services are not running")
	}
	return runtime.WebRuntime()
}

// ExportDiagnostics writes a support archive that excludes provider keys,
// session tokens, workspaces, and artifacts.
func (a *DesktopApp) ExportDiagnostics(destination string) (string, error) {
	a.mu.Lock()
	config := a.config
	status := a.statusLocked()
	a.mu.Unlock()
	return desktop.ExportDiagnostics(destination, desktopVersion, config, desktop.RuntimeStatus{
		State:      status.State,
		CouncilURL: status.CouncilURL,
	})
}

func (a *DesktopApp) OpenWorkspace(path string) error {
	cleaned, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return errors.New("workspace must be a directory")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runtime != nil {
		return errors.New("stop the desktop runtime before changing workspace")
	}
	a.workspace = cleaned
	return nil
}

// SaveProviderKey accepts only the supported provider ids and persists the key
// in the encrypted desktop secret store. It never returns the supplied value.
func (a *DesktopApp) SaveProviderKey(provider, key string) error {
	secretKey, _, ok := providerSecret(provider)
	if !ok {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("provider key is empty")
	}
	return a.store.Put(context.Background(), secretKey, []byte(key))
}

func (a *DesktopApp) statusLocked() DesktopStatus {
	status := DesktopStatus{State: desktop.StateStopped, Workspace: a.workspace, LastError: a.lastError}
	if a.runtime == nil {
		return status
	}
	runtimeStatus := a.runtime.Status()
	status.State = runtimeStatus.State
	status.CouncilURL = runtimeStatus.CouncilURL
	if runtimeStatus.LastError != "" {
		status.LastError = runtimeStatus.LastError
	}
	return status
}

func (a *DesktopApp) setStartError(err error) (DesktopStatus, error) {
	a.mu.Lock()
	a.lastError = "desktop services failed to start"
	status := a.statusLocked()
	a.mu.Unlock()
	return status, fmt.Errorf("start desktop services: %w", err)
}

func (a *DesktopApp) providerEnvironment() (map[string]string, error) {
	environment := make(map[string]string)
	for _, provider := range []string{"openai", "deepseek", "anthropic"} {
		secretKey, environmentKey, _ := providerSecret(provider)
		value, err := a.store.Get(context.Background(), secretKey)
		if errors.Is(err, desktop.ErrSecretNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s provider key: %w", provider, err)
		}
		environment[environmentKey] = string(value)
	}
	return environment, nil
}

func providerSecret(provider string) (secretKey, environmentKey string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "provider.openai", "OPENAI_API_KEY", true
	case "deepseek":
		return "provider.deepseek", "DEEPSEEK_API_KEY", true
	case "anthropic":
		return "provider.anthropic", "ANTHROPIC_API_KEY", true
	default:
		return "", "", false
	}
}
