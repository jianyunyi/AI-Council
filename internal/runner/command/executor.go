package command

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/aicouncil/aicouncil/internal/runner/pathguard"
)

type Spec struct {
	Executable  string
	Args        []string
	WorkDir     string
	Timeout     time.Duration
	OutputLimit int64
}
type Result struct {
	ExitCode       int
	Stdout, Stderr string
	Duration       time.Duration
	TimedOut       bool
}
type Executor struct{ guard *pathguard.Guard }

func NewExecutor(guard *pathguard.Guard) *Executor { return &Executor{guard: guard} }

func (e *Executor) Run(ctx context.Context, spec Spec) (Result, error) {
	if spec.Timeout <= 0 {
		spec.Timeout = 60 * time.Second
	}
	if spec.OutputLimit <= 0 {
		spec.OutputLimit = 1 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	workDir, err := e.guard.ResolveDirectory(spec.WorkDir)
	if err != nil {
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = workDir
	cmd.Env = safeEnvironment()
	stdout, stderr := &limitBuffer{limit: spec.OutputLimit}, &limitBuffer{limit: spec.OutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	started := time.Now()
	err = cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started), TimedOut: ctx.Err() == context.DeadlineExceeded}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.ExitCode = -1
	}
	if err != nil && !result.TimedOut && ctx.Err() != context.Canceled {
		return result, nil
	}
	return result, nil
}

type limitBuffer struct {
	bytes.Buffer
	limit     int64
	truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	remain := b.limit - int64(b.Len())
	if remain <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remain {
		_, _ = b.Buffer.Write(p[:remain])
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
func safeEnvironment() []string {
	out := make([]string, 0)
	for _, item := range strings.Split(strings.Join([]string{}, ""), "\x00") {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
