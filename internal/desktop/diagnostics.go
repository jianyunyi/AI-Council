package desktop

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const diagnosticLogLimit = 64 * 1024

var diagnosticSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[^\s]+`),
	regexp.MustCompile(`(?i)--token\s+[^\s]+`),
	regexp.MustCompile(`(?i)(authorization|api[_-]?key|secret|token)\s*[:=]\s*[^\s]+`),
}

type diagnosticSummary struct {
	Version string `json:"version"`
	State   string `json:"state"`
	APIBase string `json:"api_base,omitempty"`
	DataDir string `json:"data_dir"`
	LogDir  string `json:"log_dir"`
}

// ExportDiagnostics produces a support archive containing a strictly bounded,
// redacted summary and service logs. It never includes persisted secrets,
// workspace files, artifacts, or the desktop session token.
func ExportDiagnostics(destination, version string, config Config, status RuntimeStatus) (string, error) {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", fmt.Errorf("create diagnostics directory: %w", err)
	}
	archivePath := filepath.Join(destination, "ai-council-diagnostics-"+time.Now().UTC().Format("20060102T150405Z")+".zip")
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create diagnostics archive: %w", err)
	}
	writer := zip.NewWriter(file)
	writeErr := writeDiagnosticEntries(writer, version, config, status)
	closeErr := writer.Close()
	fileCloseErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", fmt.Errorf("close diagnostics archive: %w", closeErr)
	}
	if fileCloseErr != nil {
		return "", fmt.Errorf("close diagnostics file: %w", fileCloseErr)
	}
	return archivePath, nil
}

func writeDiagnosticEntries(writer *zip.Writer, version string, config Config, status RuntimeStatus) error {
	summary, err := json.Marshal(diagnosticSummary{
		Version: version,
		State:   string(status.State),
		APIBase: status.CouncilURL,
		DataDir: config.DataDir,
		LogDir:  config.LogDir,
	})
	if err != nil {
		return fmt.Errorf("encode diagnostics summary: %w", err)
	}
	if err := writeDiagnosticEntry(writer, "summary.json", summary); err != nil {
		return err
	}
	for _, name := range []string{"runner.log", "council.log"} {
		contents, err := readSanitizedDiagnosticLog(filepath.Join(config.LogDir, name))
		if err != nil {
			return err
		}
		if err := writeDiagnosticEntry(writer, name, contents); err != nil {
			return err
		}
	}
	return nil
}

func writeDiagnosticEntry(writer *zip.Writer, name string, contents []byte) error {
	entry, err := writer.Create(name)
	if err != nil {
		return fmt.Errorf("create diagnostics entry %q: %w", name, err)
	}
	if _, err := entry.Write(contents); err != nil {
		return fmt.Errorf("write diagnostics entry %q: %w", name, err)
	}
	return nil
}

func readSanitizedDiagnosticLog(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []byte("log unavailable\n"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read diagnostics log %q: %w", path, err)
	}
	if len(contents) > diagnosticLogLimit {
		contents = contents[len(contents)-diagnosticLogLimit:]
	}
	redacted := string(contents)
	for _, pattern := range diagnosticSecretPatterns {
		redacted = pattern.ReplaceAllStringFunc(redacted, redactDiagnosticMatch)
	}
	return []byte(redacted), nil
}

func redactDiagnosticMatch(match string) string {
	if strings.HasPrefix(strings.ToLower(match), "bearer") {
		return "Bearer [REDACTED]"
	}
	if strings.HasPrefix(strings.ToLower(match), "--token") {
		return "--token [REDACTED]"
	}
	separator := strings.IndexAny(match, ":=")
	if separator < 0 {
		return "[REDACTED]"
	}
	return match[:separator+1] + "[REDACTED]"
}
