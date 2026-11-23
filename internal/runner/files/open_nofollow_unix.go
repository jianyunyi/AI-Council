//go:build !windows

package files

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

var errUnsafeSymlink = errors.New("refusing to read through a symlink")

func readFileNoFollow(path string) ([]byte, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, false, errUnsafeSymlink
		}
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil, false, nil // did not exist
		}
		return nil, false, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
