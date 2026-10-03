package fsx

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// hold открывает файл так, как это делает чужая программа: share — что она
// разрешает остальным. Возвращает функцию, освобождающую файл.
func hold(t *testing.T, path string, share uint32) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(release)
	return release
}

func TestBusyRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := WriteFile(path, []byte("abc")); err != nil {
		t.Fatal(err)
	}

	release := hold(t, path, 0)
	if _, _, err := HashFile(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("HashFile занятого файла: %v, want ErrBusy", err)
	}
	if err := CopyFile(path, path+".копия"); !errors.Is(err, ErrBusy) {
		t.Fatalf("CopyFile занятого файла: %v, want ErrBusy", err)
	}

	release()
	if _, _, err := HashFile(path); err != nil {
		t.Fatalf("HashFile после освобождения: %v", err)
	}
}

func TestBusyReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := WriteFile(path, []byte("старое")); err != nil {
		t.Fatal(err)
	}

	// Обычная программа: читать и писать другим разрешает, удалять и заменять — нет.
	release := hold(t, path, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err := WriteFile(path, []byte("новое")); !errors.Is(err, ErrBusy) {
		t.Fatalf("замена занятого файла: %v, want ErrBusy", err)
	}
	if got := readFile(t, path); got != "старое" {
		t.Errorf("неудачная замена изменила файл: %q", got)
	}
	if names := dirNames(t, dir); len(names) != 1 {
		t.Errorf("неудачная замена оставила файлы: %v", names)
	}

	release()
	if err := WriteFile(path, []byte("новое")); err != nil {
		t.Fatalf("замена после освобождения: %v", err)
	}
	if got := readFile(t, path); got != "новое" {
		t.Errorf("содержимое = %q", got)
	}
}

// Короткая занятость (антивирус, индексатор) пережидается без ошибки.
func TestBusyRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := WriteFile(path, []byte("старое")); err != nil {
		t.Fatal(err)
	}

	release := hold(t, path, windows.FILE_SHARE_READ)
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()

	if err := WriteFile(path, []byte("новое")); err != nil {
		t.Fatalf("замена кратко занятого файла: %v", err)
	}
	if got := readFile(t, path); got != "новое" {
		t.Errorf("содержимое = %q", got)
	}
}
