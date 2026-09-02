//go:build windows

package desktop

import "os"

// Windows cannot reliably deliver an interrupt signal to arbitrary detached
// children. Runtime first uses each service's authenticated local shutdown
// endpoint; process termination remains the timeout fallback.
func requestOSProcessStop(_ *os.Process) error { return nil }
