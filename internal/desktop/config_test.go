package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigWindowsDefaultsToLocalAppData(t *testing.T) {
	localAppData := t.TempDir()
	councilBinary := filepath.Join(t.TempDir(), "council-server.exe")
	runnerBinary := filepath.Join(t.TempDir(), "workspace-runner.exe")

	cfg, err := LoadConfig(map[string]string{
		"GOOS":              "windows",
		"LOCALAPPDATA":      localAppData,
		"AI_COUNCIL_BINARY": councilBinary,
		"AI_RUNNER_BINARY":  runnerBinary,
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	wantDataDir := filepath.Join(localAppData, "AI-Council")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if cfg.LogDir != filepath.Join(wantDataDir, "logs") {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, filepath.Join(wantDataDir, "logs"))
	}
	if cfg.CouncilBinary != councilBinary {
		t.Errorf("CouncilBinary = %q, want %q", cfg.CouncilBinary, councilBinary)
	}
	if cfg.RunnerBinary != runnerBinary {
		t.Errorf("RunnerBinary = %q, want %q", cfg.RunnerBinary, runnerBinary)
	}
}

func TestConfigNonWindowsUsesUserConfigDirectory(t *testing.T) {
	userConfigDir := t.TempDir()

	cfg, err := LoadConfig(map[string]string{
		"GOOS":            "linux",
		"XDG_CONFIG_HOME": userConfigDir,
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	wantDataDir := filepath.Join(userConfigDir, "AI-Council")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if cfg.CouncilBinary != "council-server" {
		t.Errorf("CouncilBinary = %q, want council-server", cfg.CouncilBinary)
	}
	if cfg.RunnerBinary != "workspace-runner" {
		t.Errorf("RunnerBinary = %q, want workspace-runner", cfg.RunnerBinary)
	}
}

func TestConfigNilEnvironmentUsesRuntimeGOOS(t *testing.T) {
	nativeConfigDir := t.TempDir()
	oppositeConfigDir := t.TempDir()

	switch runtime.GOOS {
	case "windows":
		t.Setenv("GOOS", "linux")
		t.Setenv("LOCALAPPDATA", nativeConfigDir)
		t.Setenv("XDG_CONFIG_HOME", oppositeConfigDir)
	case "darwin":
		t.Setenv("GOOS", "windows")
		t.Setenv("HOME", nativeConfigDir)
		t.Setenv("LOCALAPPDATA", oppositeConfigDir)
	default:
		t.Setenv("GOOS", "windows")
		t.Setenv("XDG_CONFIG_HOME", nativeConfigDir)
		t.Setenv("LOCALAPPDATA", oppositeConfigDir)
	}

	cfg, err := LoadConfig(nil)
	if err != nil {
		t.Fatalf("LoadConfig(nil) error = %v", err)
	}

	wantConfigDir := nativeConfigDir
	if runtime.GOOS == "darwin" {
		wantConfigDir = filepath.Join(nativeConfigDir, "Library", "Application Support")
	}
	wantDataDir := filepath.Join(wantConfigDir, "AI-Council")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want runtime %s path %q", cfg.DataDir, runtime.GOOS, wantDataDir)
	}
}

func TestConfigCreatesPrivateDataAndLogDirectories(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "nested", "data")
	logDir := filepath.Join(dataDir, "private-logs")

	_, err := LoadConfig(map[string]string{
		"GOOS":                runtime.GOOS,
		"AI_COUNCIL_DATA_DIR": dataDir,
		"AI_COUNCIL_LOG_DIR":  logDir,
		"AI_COUNCIL_BINARY":   "council-test",
		"AI_RUNNER_BINARY":    "runner-test",
	})
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	for _, dir := range []string{dataDir, logDir} {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatalf("stat %q: %v", dir, statErr)
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", dir)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("permissions for %q = %o, want 700", dir, info.Mode().Perm())
		}
	}
}
