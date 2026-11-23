package main

import (
	"path/filepath"
	"testing"
)

func TestArtifactRootDefaultsBesideDatabase(t *testing.T) {
	// Mirrors the derivation in main(): <dir of db>/artifacts.
	got := filepath.Join(filepath.Dir("/app/.data/council.db"), "artifacts")
	if got != filepath.FromSlash("/app/.data/artifacts") {
		t.Fatalf("artifact root = %q, want /app/.data/artifacts", got)
	}
}
