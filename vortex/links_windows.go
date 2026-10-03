package vortex

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procFindFirstFileName = modkernel32.NewProc("FindFirstFileNameW")
	procFindNextFileName  = modkernel32.NewProc("FindNextFileNameW")
)

// hardLinks перечисляет все имена файла на его томе — полными путями.
func hardLinks(path string) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(abs)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	h, _, callErr := procFindFirstFileName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	if windows.Handle(h) == windows.InvalidHandle {
		return nil, callErr
	}
	defer windows.FindClose(windows.Handle(h))

	var names []string
	for {
		names = append(names, volume+windows.UTF16ToString(buf[:size]))
		size = uint32(len(buf))
		ok, _, _ := procFindNextFileName.Call(h, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
		if ok == 0 {
			return names, nil
		}
	}
}
