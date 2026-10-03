package fsx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Link создаёт жёсткую ссылку dst на файл src. Если dst уже существует,
// возвращает ошибку, совместимую с fs.ErrExist. Разные тома — ErrCrossVolume,
// файловая система без ссылок — ErrLinkUnsupported.
func Link(src, dst string) error {
	err := withRetry(src, func() error { return os.Link(src, dst) })
	return linkError(err)
}

// CanLink проверяет на деле, можно ли связать жёсткой ссылкой файлы из
// srcDir и dstDir: создаёт пробный файл и ссылку на него, затем удаляет оба.
// Обе папки должны существовать. nil означает «можно».
func CanLink(srcDir, dstDir string) error {
	f, err := os.CreateTemp(srcDir, tempPrefix+"*"+tempSuffix)
	if err != nil {
		return err
	}
	src := f.Name()
	f.Close()
	defer os.Remove(src)

	// Имя подходит под шаблон временных файлов: если пробу оборвут, её уберёт CleanTemp.
	dst := filepath.Join(dstDir, strings.TrimSuffix(filepath.Base(src), tempSuffix)+"-link"+tempSuffix)
	if err := Link(src, dst); err != nil {
		return err
	}
	return os.Remove(dst)
}

// SameVolume сообщает, лежат ли пути на одном томе. Пути могут ещё не
// существовать: том определяется по ближайшей существующей папке.
// Это быстрая оценка; окончательный ответ даёт CanLink.
func SameVolume(a, b string) (bool, error) {
	ida, err := pathVolumeID(a)
	if err != nil {
		return false, err
	}
	idb, err := pathVolumeID(b)
	if err != nil {
		return false, err
	}
	return ida == idb, nil
}

func pathVolumeID(path string) (uint64, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	for {
		_, err := os.Stat(p)
		if err == nil {
			return volumeID(p)
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return 0, err
		}
		p = parent
	}
}

// CopyFile атомарно копирует src в dst: dst либо остаётся прежним, либо
// становится полной копией. Запасной путь там, где ссылки невозможны.
func CopyFile(src, dst string) error {
	in, err := openRead(src)
	if err != nil {
		return err
	}
	defer in.Close()

	w, err := Create(dst)
	if err != nil {
		return err
	}
	defer w.Abort()
	if _, err := io.Copy(w, in); err != nil {
		return err
	}
	return w.Commit()
}
