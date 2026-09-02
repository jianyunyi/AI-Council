package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const appDirectoryName = "AI-Council"

type Config struct {
	DataDir       string
	LogDir        string
	CouncilBinary string
	RunnerBinary  string
}

func LoadConfig(env map[string]string) (Config, error) {
	getenv := func(key string) string {
		if env == nil {
			return os.Getenv(key)
		}
		return env[key]
	}

	goos := getenv("GOOS")
	if goos == "" {
		goos = runtime.GOOS
	}

	dataDir := getenv("AI_COUNCIL_DATA_DIR")
	if dataDir == "" {
		configDir, err := userConfigDir(goos, getenv)
		if err != nil {
			return Config{}, err
		}
		dataDir = filepath.Join(configDir, appDirectoryName)
	}

	logDir := getenv("AI_COUNCIL_LOG_DIR")
	if logDir == "" {
		logDir = filepath.Join(dataDir, "logs")
	}

	councilBinary := getenv("AI_COUNCIL_BINARY")
	if councilBinary == "" {
		councilBinary = executableName("council-server", goos)
	}
	runnerBinary := getenv("AI_RUNNER_BINARY")
	if runnerBinary == "" {
		runnerBinary = executableName("workspace-runner", goos)
	}

	for _, dir := range []string{dataDir, logDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Config{}, fmt.Errorf("create desktop directory %q: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return Config{}, fmt.Errorf("secure desktop directory %q: %w", dir, err)
		}
	}

	return Config{
		DataDir:       dataDir,
		LogDir:        logDir,
		CouncilBinary: councilBinary,
		RunnerBinary:  runnerBinary,
	}, nil
}

func userConfigDir(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		if dir := getenv("LOCALAPPDATA"); dir != "" {
			return dir, nil
		}
		return "", fmt.Errorf("determine user config directory: LOCALAPPDATA is not set")
	case "darwin":
		if home := getenv("HOME"); home != "" {
			return filepath.Join(home, "Library", "Application Support"), nil
		}
	default:
		if dir := getenv("XDG_CONFIG_HOME"); dir != "" {
			return dir, nil
		}
		if home := getenv("HOME"); home != "" {
			return filepath.Join(home, ".config"), nil
		}
	}
	return "", fmt.Errorf("determine user config directory: home directory is not set")
}

func executableName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
