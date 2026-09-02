package desktop

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestExecCommandStarterRunsAndGracefullyStopsRealChild(t *testing.T) {
	helper := buildProcessHelper(t)
	listener, err := AllocateLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	process, err := (ExecCommandStarter{}).Start(context.Background(), ProcessSpec{
		Name:   helper,
		Args:   []string{"--listen", address, "--token", "integration-token"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = process.Kill() })

	baseURL := "http://" + address
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, healthErr := http.Get(baseURL + "/healthz")
		if healthErr == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not become healthy: %v", healthErr)
		}
		time.Sleep(25 * time.Millisecond)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/shutdown", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer integration-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request child shutdown: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("shutdown status = %d, want 202", response.StatusCode)
	}

	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("child exit error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit after graceful shutdown")
	}
}

func buildProcessHelper(t *testing.T) string {
	t.Helper()
	name := "desktop-process-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	helper := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", helper, "./testdata/processhelper")
	command.Dir = "."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build child helper: %v\n%s", err, output)
	}
	return helper
}
