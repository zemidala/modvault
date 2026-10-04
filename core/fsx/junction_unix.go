//go:build unix

package fsx

import "os"

// LinkDir создаёт ссылку на папку.
func LinkDir(target, link string) error {
	if fi, err := os.Stat(target); err != nil {
		return err
	} else if !fi.IsDir() {
		return &os.PathError{Op: "link", Path: target, Err: os.ErrInvalid}
	}
	return os.Symlink(target, link)
}
