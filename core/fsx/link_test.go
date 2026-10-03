package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skipIfNoLinks пропускает тест там, где временная папка лежит на файловой
// системе без жёстких ссылок.
func skipIfNoLinks(t *testing.T, dir string) {
	t.Helper()
	if err := CanLink(dir, dir); errors.Is(err, ErrLinkUnsupported) {
		t.Skipf("жёсткие ссылки недоступны: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
}

func TestLink(t *testing.T) {
	dir := t.TempDir()
	skipIfNoLinks(t, dir)

	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := WriteFile(src, []byte("данные")); err != nil {
		t.Fatal(err)
	}
	if err := Link(src, dst); err != nil {
		t.Fatal(err)
	}

	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(si, di) {
		t.Error("ссылка указывает на другой файл")
	}

	if err := Link(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Errorf("ссылка поверх существующего файла: %v, want fs.ErrExist", err)
	}
	if err := Link(filepath.Join(dir, "нет.txt"), filepath.Join(dir, "x.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ссылка на несуществующий файл: %v, want fs.ErrNotExist", err)
	}

	// Удаление одного имени не трогает данные под другим.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != "данные" {
		t.Errorf("после удаления источника содержимое = %q", got)
	}
}

func TestCanLink(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	skipIfNoLinks(t, a)

	if err := CanLink(a, b); err != nil {
		t.Fatalf("CanLink между соседними папками: %v", err)
	}
	for _, dir := range []string{a, b} {
		if names := dirNames(t, dir); len(names) != 0 {
			t.Errorf("CanLink оставил файлы в %s: %v", dir, names)
		}
	}

	if err := CanLink(a, filepath.Join(b, "нет")); err == nil {
		t.Error("CanLink в несуществующую папку прошёл без ошибки")
	}
	if names := dirNames(t, a); len(names) != 0 {
		t.Errorf("после неудачной пробы остались файлы: %v", names)
	}
}

func TestSameVolume(t *testing.T) {
	dir := t.TempDir()
	same, err := SameVolume(dir, filepath.Join(dir, "ещё", "нет", "такой.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Error("папка и путь внутри неё оказались на разных томах")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	content := strings.Repeat("0123456789", 100_000)
	if err := WriteFile(src, []byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dst, []byte("прежнее")); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dst); got != content {
		t.Errorf("копия отличается от оригинала: %d байт", len(got))
	}

	si, _ := os.Stat(src)
	di, _ := os.Stat(dst)
	if os.SameFile(si, di) {
		t.Error("CopyFile создал ссылку, а не копию")
	}

	if err := CopyFile(filepath.Join(dir, "нет.bin"), dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("копирование несуществующего файла: %v, want fs.ErrNotExist", err)
	}
	if got := readFile(t, dst); got != content {
		t.Error("неудачное копирование испортило целевой файл")
	}
	if names := dirNames(t, dir); len(names) != 2 {
		t.Errorf("в папке остались лишние файлы: %v", names)
	}
}
