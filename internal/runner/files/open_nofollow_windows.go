//go:build windows

package files

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

var errUnsafeSymlink = errors.New("refusing to read through a reparse point")

func readFileNoFollow(path string) ([]byte, bool, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, false, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, false, errUnsafeSymlink
	}
	f := os.NewFile(uintptr(handle), path)
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
