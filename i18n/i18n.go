// Package i18n — язык интерфейса.
//
// Исходный язык программы — русский: строки в коде написаны по-русски и
// служат ключами словаря. Для другого языка строка ищется в словаре; если
// перевода нет, остаётся русская. Так работают T, Sprintf, Errorf и NewError.
//
// Язык выбирается один раз при запуске (см. Language) и до конца работы не
// меняется: часть строк составляется при загрузке программы.
package i18n

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Языки интерфейса.
const (
	Russian = "ru"
	English = "en"
)

// languageFile — где лежит выбор языка: рядом с настройками окна.
const languageFile = "language"

var current = Russian

func init() {
	// В тестах язык всегда исходный: тесты сверяют русские сообщения.
	if testing.Testing() {
		return
	}
	current = saved()
}

// saved читает выбранный язык; без выбора — русский.
func saved() string {
	if env := os.Getenv("MODVAULT_LANG"); env != "" {
		return normal(env)
	}
	path, err := file()
	if err != nil {
		return Russian
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Russian
	}
	return normal(string(data))
}

func normal(code string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(code)), English) {
		return English
	}
	return Russian
}

// fileOverride — файл выбора языка для тестов; пусто — настоящий.
var fileOverride string

// UseFile задаёт, где лежит выбор языка. Для тестов: они не должны менять
// язык настоящей программы.
func UseFile(path string) { fileOverride = path }

func file() (string, error) {
	if fileOverride != "" {
		return fileOverride, nil
	}
	if testing.Testing() {
		return "", errors.New("в тестах выбор языка не хранится")
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Modvault", languageFile), nil
}

// Language возвращает язык интерфейса этого запуска.
func Language() string { return current }

// Saved возвращает язык, выбранный для следующего запуска.
func Saved() string { return saved() }

// Save запоминает язык для следующих запусков программы.
func Save(code string) error {
	path, err := file()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(normal(code)+"\n"), 0o644)
}

// Use задаёт язык этого запуска. Только для тестов и командной строки:
// строки, составленные при загрузке программы, уже не изменятся.
func Use(code string) { current = normal(code) }

// T возвращает строку на языке интерфейса.
func T(s string) string {
	if current == English {
		if t, ok := en[s]; ok {
			return t
		}
	}
	return s
}

// Sprintf — fmt.Sprintf с переводом формата.
func Sprintf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// Fprintf — fmt.Fprintf с переводом формата.
func Fprintf(w io.Writer, format string, args ...any) (int, error) {
	return fmt.Fprintf(w, T(format), args...)
}

// Errorf — fmt.Errorf с переводом формата; %w работает как обычно.
func Errorf(format string, args ...any) error {
	return fmt.Errorf(T(format), args...)
}

// message — ошибка, текст которой переводится в момент показа: такие ошибки
// создаются и при загрузке программы, когда язык ещё не выбран.
type message struct{ text string }

func (m *message) Error() string { return T(m.text) }

// NewError — errors.New с переводом текста. Каждая такая ошибка — отдельное
// значение, так что errors.Is различает их как обычно.
func NewError(text string) error { return &message{text} }

// Quote берёт название в кавычки, принятые в языке интерфейса.
func Quote(name string) string {
	if current == English {
		return "“" + name + "”"
	}
	return "«" + name + "»"
}

// Plural выбирает форму слова по числу. Формы даются по-русски: 1 мод,
// 2 мода, 5 модов; для английского они переводятся парой «one|other».
func Plural(n int, one, few, many string) string {
	if current == English {
		if t, ok := en[one+"|"+few+"|"+many]; ok {
			single, other, _ := strings.Cut(t, "|")
			if n == 1 {
				return single
			}
			return other
		}
	}
	n %= 100
	if n < 0 {
		n = -n
	}
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
