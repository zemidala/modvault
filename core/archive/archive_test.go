package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zemidala/modvault/core/fsx"
)

// writeZip создаёт zip с файлами name → содержимое; имя с «/» на конце — папка.
func writeZip(t *testing.T, files [][2]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mod.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, file := range files {
		// CreateHeader, а не Create: тот отказывается от некоторых вредных имён,
		// а нам нужно проверить собственную защиту.
		entry, err := w.CreateHeader(&zip.FileHeader{Name: file[0], Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func paths(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	sort.Strings(out)
	return out
}

func checkModFiles(t *testing.T, dest string, entries []Entry) {
	t.Helper()
	want := []string{"Мод/scripts/файл.lua", "Мод/Мод.mod"}
	sort.Strings(want)
	if got := paths(entries); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("файлы = %v, want %v", got, want)
	}
	data, err := os.ReadFile(filepath.Join(dest, "Мод", "scripts", "файл.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "return {}\n" {
		t.Errorf("содержимое = %q", data)
	}
}

func TestExtractZip(t *testing.T) {
	path := writeZip(t, [][2]string{
		{"Мод/", ""},
		{"Мод/Мод.mod", "return {}\n"},
		{`Мод\scripts\файл.lua`, "return {}\n"},
	})
	dest := filepath.Join(t.TempDir(), "out")
	entries, err := Extract(path, dest, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	checkModFiles(t, dest, entries)
	for _, e := range entries {
		if e.Size != int64(len("return {}\n")) {
			t.Errorf("%s: размер %d", e.Path, e.Size)
		}
	}
}

// Архив testdata/mod.7z создан программой 7-Zip из тех же двух файлов.
func TestExtractSevenZip(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	entries, err := Extract(filepath.Join("testdata", "mod.7z"), dest, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	checkModFiles(t, dest, entries)
}

func TestExtractRar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mod.rar")
	data := buildRar([][2]string{
		{"Мод/Мод.mod", "return {}\n"},
		{"Мод/scripts/файл.lua", "return {}\n"},
	})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	entries, err := Extract(path, dest, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	checkModFiles(t, dest, entries)
}

func TestExtractRejects(t *testing.T) {
	small := Limits{MaxFiles: 2, MaxFileSize: 10, MaxTotalSize: 15}
	tests := []struct {
		name  string
		files [][2]string
		lim   Limits
		want  error
	}{
		{"выход из папки", [][2]string{{"ok.txt", "x"}, {"../evil.txt", "x"}}, DefaultLimits, fsx.ErrUnsafePath},
		{"абсолютный путь", [][2]string{{"/evil.txt", "x"}}, DefaultLimits, fsx.ErrUnsafePath},
		{"буква диска", [][2]string{{`C:\evil.txt`, "x"}}, DefaultLimits, fsx.ErrUnsafePath},
		{"имя устройства", [][2]string{{"dir/NUL.txt", "x"}}, DefaultLimits, fsx.ErrUnsafePath},
		{"слишком много файлов", [][2]string{{"a", "x"}, {"b", "x"}, {"c", "x"}}, small, ErrTooLarge},
		{"слишком большой файл", [][2]string{{"a", "12345678901"}}, small, ErrTooLarge},
		{"слишком большой архив", [][2]string{{"a", "1234567890"}, {"b", "123456"}}, small, ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "out")
			_, err := Extract(writeZip(t, tt.files), dest, tt.lim)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ошибка = %v, want %v", err, tt.want)
			}
			// Ничего не должно оказаться выше папки назначения.
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); err == nil {
				t.Error("файл записан за пределы папки назначения")
			}
		})
	}
}

func TestExtractDuplicate(t *testing.T) {
	path := writeZip(t, [][2]string{{"a.txt", "1"}, {"a.txt", "2"}})
	if _, err := Extract(path, filepath.Join(t.TempDir(), "out"), DefaultLimits); err == nil {
		t.Error("архив с повтором имени распаковался без ошибки")
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	notArchive := filepath.Join(dir, "мод.zip")
	if err := os.WriteFile(notArchive, []byte("это не архив"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(notArchive); !errors.Is(err, ErrFormat) {
		t.Errorf("текст с расширением zip: %v, want ErrFormat", err)
	}
	if _, err := Extract(notArchive, filepath.Join(dir, "out"), DefaultLimits); !errors.Is(err, ErrFormat) {
		t.Errorf("Extract текста: %v, want ErrFormat", err)
	}

	empty := filepath.Join(dir, "пустой.zip")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(empty); !errors.Is(err, ErrFormat) {
		t.Errorf("пустой файл: %v, want ErrFormat", err)
	}

	// Формат определяется по содержимому: zip с чужим расширением — всё равно zip.
	renamed := filepath.Join(dir, "мод.rar")
	data, err := os.ReadFile(writeZip(t, [][2]string{{"a.txt", "x"}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(renamed, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Detect(renamed); err != nil || got != Zip {
		t.Errorf("zip с расширением rar: %q, %v", got, err)
	}
}

// buildRar собирает архив RAR5 без сжатия. Программы для создания rar под
// рукой нет, а проверить чтение настоящим декодером нужно.
func buildRar(files [][2]string) []byte {
	vint := func(n uint64) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n == 0 {
				return append(out, b)
			}
			out = append(out, b|0x80)
		}
	}
	// header оборачивает тело заголовка: CRC32, размер, тело.
	header := func(body []byte) []byte {
		sized := append(vint(uint64(len(body))), body...)
		out := binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(sized))
		return append(out, sized...)
	}

	var buf bytes.Buffer
	buf.WriteString("Rar!\x1a\x07\x01\x00")
	buf.Write(header([]byte{1, 0, 0})) // главный заголовок: тип 1, без флагов

	for _, file := range files {
		name, data := []byte(file[0]), []byte(file[1])
		var body []byte
		body = append(body, 2)                          // тип: файл
		body = append(body, 0x02)                       // флаги заголовка: есть данные
		body = append(body, vint(uint64(len(data)))...) // размер данных
		body = append(body, 0x04)                       // флаги файла: есть CRC32
		body = append(body, vint(uint64(len(data)))...) // размер после распаковки
		body = append(body, 0x20)                       // атрибуты: обычный файл
		body = binary.LittleEndian.AppendUint32(body, crc32.ChecksumIEEE(data))
		body = append(body, 0)                          // без сжатия
		body = append(body, 0)                          // система: Windows
		body = append(body, vint(uint64(len(name)))...) // длина имени
		body = append(body, name...)
		buf.Write(header(body))
		buf.Write(data)
	}

	buf.Write(header([]byte{5, 0, 0})) // конец архива
	return buf.Bytes()
}
