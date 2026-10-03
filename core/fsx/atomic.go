package fsx

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zemidala/modvault/i18n"
)

// Временные файлы лежат рядом с целевым: переименование атомарно только
// в пределах одного тома. Имя не зависит от целевого, чтобы не упереться
// в предел длины имени.
const (
	tempPrefix = ".mv-"
	tempSuffix = ".tmp"
)

// Writer пишет файл так, что по целевому пути всегда лежит либо прежнее
// содержимое, либо новое целиком. Данные идут во временный файл, Commit
// сбрасывает его на диск и переименовывает поверх целевого.
type Writer struct {
	f    *os.File
	path string
	done bool
}

// Create начинает атомарную запись файла path. Папка должна существовать.
// После Create нужно вызвать Commit или Abort; обычно — defer w.Abort().
func Create(path string) (*Writer, error) {
	f, err := os.CreateTemp(filepath.Dir(path), tempPrefix+"*"+tempSuffix)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return &Writer{f: f, path: path}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

// Commit завершает запись: после успешного возврата новое содержимое лежит
// по целевому пути и сброшено на диск. При ошибке целевой файл не тронут,
// временный удалён. Если целевой файл занят — ErrBusy.
func (w *Writer) Commit() error {
	if w.done {
		return i18n.NewError("fsx: запись уже завершена")
	}
	w.done = true

	tmp := w.f.Name()
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = withRetry(w.path, func() error { return os.Rename(tmp, w.path) })
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(w.path))
}

// Abort отменяет запись и удаляет временный файл. После Commit ничего не делает.
func (w *Writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.f.Close()
	return os.Remove(w.f.Name())
}

// WriteFile атомарно записывает data в файл path.
func WriteFile(path string, data []byte) error {
	w, err := Create(path)
	if err != nil {
		return err
	}
	defer w.Abort()
	if _, err := w.Write(data); err != nil {
		return err
	}
	return w.Commit()
}

// CleanTemp удаляет из папки dir временные файлы, оставшиеся после обрыва
// записи, и возвращает их число. Вложенные папки не просматривает.
// Вызывать, пока в dir никто не пишет.
func CleanTemp(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, tempPrefix) || !strings.HasSuffix(name, tempSuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
