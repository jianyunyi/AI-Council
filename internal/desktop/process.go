package desktop

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
)

// ProcessSpec describes a child process without exposing an exec.Cmd to the
// runtime. Env is an overlay on the desktop process environment.
type ProcessSpec struct {
	Name   string
	Args   []string
	Env    map[string]string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

// Process is the lifecycle surface Runtime needs from a child process.
type Process interface {
	Wait() error
	Stop() error
	Kill() error
}

// CommandStarter starts a child process from a sanitized process spec.
type CommandStarter interface {
	Start(context.Context, ProcessSpec) (Process, error)
}

// ExecCommandStarter starts operating-system processes.
type ExecCommandStarter struct{}

func (ExecCommandStarter) Start(ctx context.Context, spec ProcessSpec) (Process, error) {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnvironment(spec.Env)
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &commandProcess{cmd: cmd}, nil
}

type commandProcess struct {
	cmd *exec.Cmd
}

func (p *commandProcess) Wait() error {
	return p.cmd.Wait()
}

func (p *commandProcess) Stop() error {
	return requestOSProcessStop(p.cmd.Process)
}

func (p *commandProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func mergedEnvironment(overlay map[string]string) []string {
	if len(overlay) == 0 {
		return os.Environ()
	}

	environment := make(map[string]string, len(os.Environ())+len(overlay))
	for _, entry := range os.Environ() {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				environment[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	for key, value := range overlay {
		environment[key] = value
	}

	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}
