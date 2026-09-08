//go:build !windows

package context

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: %s", errUnsafePath, path)
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
