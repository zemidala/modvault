package fsx

import (
	"os"
	"path/filepath"
)

// Trash переносит файл или папку в Корзину. Если это невозможно (Корзина
// отключена, сетевой диск, слишком длинный путь), возвращает
// ErrTrashUnavailable и оставляет всё на месте: насовсем Trash не удаляет.
func Trash(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err
	}
	return trash(abs)
}
