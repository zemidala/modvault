//go:build unix

package fsx

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// withRetry вне Windows просто выполняет операцию: открытый файл там
// не мешает ни переименованию, ни удалению.
func withRetry(_ string, op func() error) error {
	return op()
}

// syncDir сбрасывает на диск запись о переименовании.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

func volumeID(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return uint64(st.Dev), nil
}

func linkError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EXDEV):
		return fmt.Errorf("%w: %w", ErrCrossVolume, err)
	case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("%w: %w", ErrLinkUnsupported, err)
	}
	return err
}

func trash(path string) error {
	return fmt.Errorf("%w: %s", ErrTrashUnavailable, path)
}
