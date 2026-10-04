package fsx

import (
	"os"
	"path/filepath"
	"strings"
)

// LinkTarget сообщает, куда ведёт ссылка на папку (junction или
// символическая ссылка); false — по этому пути не ссылка.
func LinkTarget(path string) (string, bool) {
	target, err := os.Readlink(path)
	if err != nil || target == "" {
		return "", false
	}
	return target, true
}

// SamePath сравнивает пути без учёта регистра и завершающих разделителей.
func SamePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// RemoveLink убирает ссылку на папку, не трогая то, на что она ведёт.
// Обычную папку не удаляет: возвращает ошибку.
func RemoveLink(path string) error {
	if _, ok := LinkTarget(path); !ok {
		return &os.PathError{Op: "remove link", Path: path, Err: os.ErrInvalid}
	}
	// os.Remove снимает саму ссылку; RemoveAll здесь недопустим.
	return os.Remove(path)
}
