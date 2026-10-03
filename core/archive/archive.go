// Package archive — распаковка архивов модов (zip, 7z, rar) с защитой от
// вредных архивов: лимиты размера и запрет выхода за папку назначения.
package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/i18n"
)

var (
	// ErrFormat — файл не является архивом известного формата.
	ErrFormat = i18n.NewError("неизвестный формат архива")
	// ErrTooLarge — архив превышает лимиты распаковки.
	ErrTooLarge = i18n.NewError("архив превышает допустимый размер")
)

// Format — формат архива, определённый по содержимому, а не по расширению.
type Format string

const (
	Zip      Format = "zip"
	SevenZip Format = "7z"
	Rar      Format = "rar"
)

// Limits ограничивает распаковку. Архив, не укладывающийся в лимиты,
// отклоняется целиком.
type Limits struct {
	MaxFiles     int
	MaxFileSize  int64
	MaxTotalSize int64
}

// DefaultLimits с запасом покрывают самые большие моды.
var DefaultLimits = Limits{MaxFiles: 20_000, MaxFileSize: 500 << 20, MaxTotalSize: 1 << 30}

// Entry — распакованный файл: путь относительно папки назначения через «/».
type Entry struct {
	Path string
	Size int64
}

// Detect определяет формат архива по первым байтам файла.
func Detect(path string) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	head := make([]byte, 8)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return Zip, nil
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return SevenZip, nil
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		return Rar, nil
	}
	return "", fmt.Errorf("%w: %s", ErrFormat, path)
}

// Extract распаковывает архив в папку dest, которая должна быть пустой или
// не существовать. При любой ошибке архив считается отклонённым; то, что
// успело распаковаться, остаётся в dest — убирает вызывающий.
func Extract(path, dest string, lim Limits) ([]Entry, error) {
	format, err := Detect(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}

	x := &extractor{dest: dest, lim: lim}
	switch format {
	case Zip:
		err = x.zip(path)
	case SevenZip:
		err = x.sevenZip(path)
	case Rar:
		err = x.rar(path)
	}
	if err != nil {
		return nil, i18n.Errorf("распаковка %s: %w", filepath.Base(path), err)
	}
	return x.entries, nil
}

type extractor struct {
	dest    string
	lim     Limits
	entries []Entry
	total   int64
}

func (x *extractor) zip(path string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := x.add(f.Name, f.Mode(), f.Open); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) sevenZip(path string) error {
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := x.add(f.Name, f.Mode(), f.Open); err != nil {
			return err
		}
	}
	return nil
}

func (x *extractor) rar(path string) error {
	r, err := rardecode.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// rar читается потоком: содержимое текущего файла отдаёт сам r.
		open := func() (io.ReadCloser, error) { return io.NopCloser(r), nil }
		if err := x.add(h.Name, h.Mode(), open); err != nil {
			return err
		}
	}
}

// add распаковывает одну запись архива.
func (x *extractor) add(name string, mode fs.FileMode, open func() (io.ReadCloser, error)) error {
	target, err := fsx.SafeJoin(x.dest, name)
	if err != nil {
		return err
	}
	if mode.IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if !mode.IsRegular() {
		return i18n.Errorf("%q: ссылки и особые файлы в архиве не поддерживаются", name)
	}
	if len(x.entries) >= x.lim.MaxFiles {
		return i18n.Errorf("%w: больше %d файлов", ErrTooLarge, x.lim.MaxFiles)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := open()
	if err != nil {
		return err
	}
	defer rc.Close()

	// O_EXCL ловит повторы имён, в том числе отличающиеся только регистром.
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return i18n.Errorf("%q: имя встречается в архиве дважды", name)
	}
	if err != nil {
		return err
	}

	// Заявленному в архиве размеру не верим: считаем то, что реально записали.
	limit := min(x.lim.MaxFileSize, x.lim.MaxTotalSize-x.total)
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("%w: %q", ErrTooLarge, name)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	rel, err := filepath.Rel(x.dest, target)
	if err != nil {
		return err
	}
	x.total += n
	x.entries = append(x.entries, Entry{Path: filepath.ToSlash(rel), Size: n})
	return nil
}
