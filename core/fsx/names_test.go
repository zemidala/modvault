package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exercise прогоняет все операции пакета в папке dir.
func exercise(t *testing.T, dir string) {
	t.Helper()

	path, err := SafeJoin(dir, "вложенная/файл ё.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("abc")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := WriteFile(path, []byte("abc")); err != nil {
		t.Fatalf("WriteFile поверх: %v", err)
	}
	h, n, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if n != 3 || h.String() != abcHash {
		t.Errorf("HashFile = %s, %d", h, n)
	}

	copied := path + ".копия"
	if err := CopyFile(path, copied); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if got := readFile(t, copied); got != "abc" {
		t.Errorf("копия = %q", got)
	}

	same, err := SameVolume(path, copied)
	if err != nil {
		t.Fatalf("SameVolume: %v", err)
	}
	if !same {
		t.Error("SameVolume = false для файлов в одной папке")
	}

	linked := path + ".ссылка"
	switch err := Link(path, linked); {
	case errors.Is(err, ErrLinkUnsupported):
		t.Logf("жёсткие ссылки недоступны: %v", err)
	case err != nil:
		t.Fatalf("Link: %v", err)
	default:
		if got := readFile(t, linked); got != "abc" {
			t.Errorf("ссылка = %q", got)
		}
	}

	w, err := Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}

func TestCyrillicPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Моды", "Тёмный прилив")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exercise(t, dir)
}

func TestLongPaths(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 400 {
		dir = filepath.Join(dir, strings.Repeat("длинная-папка-", 3))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exercise(t, dir)
}
