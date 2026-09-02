//go:build !windows

package desktop

import (
	"errors"
	"os"
)

func requestOSProcessStop(process *os.Process) error {
	if process == nil {
		return nil
	}
	err := process.Signal(os.Interrupt)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
