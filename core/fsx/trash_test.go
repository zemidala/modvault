package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrash(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "modvault-тест-корзины.txt")
	if err := WriteFile(file, []byte("x")); err != nil {
		t.Fatal(err)
	}
	err := Trash(file)
	if errors.Is(err, ErrTrashUnavailable) {
		if _, serr := os.Stat(file); serr != nil {
			t.Fatalf("Корзина недоступна, но файл пропал: %v", serr)
		}
		t.Skipf("Корзина здесь недоступна: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("после Trash файл на месте: %v", err)
	}

	folder := filepath.Join(dir, "modvault-тест-корзины")
	if err := os.MkdirAll(filepath.Join(folder, "вложенная"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(folder, "вложенная", "a.txt"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := Trash(folder); err != nil {
		t.Fatalf("Trash папки: %v", err)
	}
	if _, err := os.Stat(folder); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("после Trash папка на месте: %v", err)
	}

	if err := Trash(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Trash несуществующего файла: %v, want fs.ErrNotExist", err)
	}
}

// Если Корзина путь не принимает, файл обязан остаться на месте.
func TestTrashLongPath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 400 {
		dir = filepath.Join(dir, strings.Repeat("длинная-папка-", 3))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.txt")
	if err := WriteFile(file, []byte("x")); err != nil {
		t.Fatal(err)
	}

	err := Trash(file)
	_, serr := os.Stat(file)
	switch {
	case err == nil && !errors.Is(serr, fs.ErrNotExist):
		t.Errorf("Trash вернул успех, а файл на месте")
	case errors.Is(err, ErrTrashUnavailable) && serr != nil:
		t.Errorf("Trash вернул ErrTrashUnavailable, но файл пропал: %v", serr)
	case err != nil && !errors.Is(err, ErrTrashUnavailable):
		t.Errorf("Trash: %v", err)
	}
}
