package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/aicouncil/aicouncil/internal/desktop"
)

func main() {
	config, err := desktop.LoadConfig(nil)
	if err != nil {
		log.Fatal(err)
	}
	config = withBundledServiceBinaries(config)
	store, err := desktop.NewSecretStore(filepath.Join(config.DataDir, "secrets"))
	if err != nil {
		log.Fatal(err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	app, err := NewDesktopApp(config, store, workspace)
	if err != nil {
		log.Fatal(err)
	}
	if err := runWails(app); err != nil {
		log.Fatal(err)
	}
}

func withBundledServiceBinaries(config desktop.Config) desktop.Config {
	executable, err := os.Executable()
	if err != nil {
		return config
	}
	bundleDir := filepath.Dir(executable)
	if os.Getenv("AI_COUNCIL_BINARY") == "" {
		candidate := filepath.Join(bundleDir, filepath.Base(config.CouncilBinary))
		if _, err := os.Stat(candidate); err == nil {
			config.CouncilBinary = candidate
		}
	}
	if os.Getenv("AI_RUNNER_BINARY") == "" {
		candidate := filepath.Join(bundleDir, filepath.Base(config.RunnerBinary))
		if _, err := os.Stat(candidate); err == nil {
			config.RunnerBinary = candidate
		}
	}
	return config
}
