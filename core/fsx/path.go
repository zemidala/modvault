package fsx

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zemidala/modvault/i18n"
)

// SafeJoin приклеивает относительный путь rel к root и гарантирует, что
// результат лежит внутри root. Разделителем в rel считается и «/», и «\».
//
// Имена проверяются по правилам Windows на любой системе: моды ставятся
// в игру для Windows, и архив с именем вроде «NUL» или «a:b» там опасен.
// Символические ссылки внутри root не раскрываются.
func SafeJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", unsafePath(rel, i18n.T("пустой путь"))
	}
	if isSeparator(rune(rel[0])) {
		return "", unsafePath(rel, i18n.T("абсолютный путь"))
	}

	elems := []string{root}
	for _, name := range strings.FieldsFunc(rel, isSeparator) {
		if name == "." {
			continue
		}
		if reason := checkName(name); reason != "" {
			return "", unsafePath(rel, reason)
		}
		elems = append(elems, name)
	}
	if len(elems) == 1 {
		return "", unsafePath(rel, i18n.T("пустой путь"))
	}
	return filepath.Join(elems...), nil
}

func unsafePath(rel, reason string) error {
	return fmt.Errorf("%w: %q: %s", ErrUnsafePath, rel, reason)
}

func isSeparator(r rune) bool {
	return r == '/' || r == '\\'
}

// checkName возвращает причину, по которой имя недопустимо, или пустую строку.
func checkName(name string) string {
	if name == ".." {
		return i18n.T("выход из папки")
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
			return i18n.T("недопустимый символ в имени")
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return i18n.T("имя оканчивается точкой или пробелом")
	}
	if isReservedName(name) {
		return i18n.T("зарезервированное имя устройства")
	}
	return ""
}

// isReservedName сообщает, совпадает ли имя с устройством Windows.
// Расширение не спасает: «NUL.txt» тоже открывает устройство.
func isReservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		return stem[3] >= '1' && stem[3] <= '9'
	}
	return false
}
