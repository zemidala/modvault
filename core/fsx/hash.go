package fsx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

const hashPrefix = "sha256:"

// Hash — контрольная сумма содержимого файла (SHA-256).
// В текстовом виде несёт имя алгоритма: «sha256:<hex>».
type Hash [sha256.Size]byte

func (h Hash) String() string {
	return hashPrefix + hex.EncodeToString(h[:])
}

// IsZero сообщает, что хеш не задан.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// MarshalText и UnmarshalText позволяют хранить хеш строкой в JSON.
func (h Hash) MarshalText() ([]byte, error) {
	return []byte(h.String()), nil
}

func (h *Hash) UnmarshalText(text []byte) error {
	parsed, err := ParseHash(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

// ParseHash разбирает строку вида «sha256:<hex>».
func ParseHash(s string) (Hash, error) {
	var h Hash
	digits, ok := strings.CutPrefix(s, hashPrefix)
	if !ok || len(digits) != hex.EncodedLen(len(h)) {
		return Hash{}, fmt.Errorf("fsx: неверный хеш %q", s)
	}
	if _, err := hex.Decode(h[:], []byte(digits)); err != nil {
		return Hash{}, fmt.Errorf("fsx: неверный хеш %q", s)
	}
	return h, nil
}

// HashReader читает r до конца и возвращает хеш и число прочитанных байт.
func HashReader(r io.Reader) (Hash, int64, error) {
	hasher := sha256.New()
	n, err := io.Copy(hasher, r)
	if err != nil {
		return Hash{}, n, err
	}
	var h Hash
	hasher.Sum(h[:0])
	return h, n, nil
}

// HashFile возвращает хеш и размер файла. Если файл занят — ErrBusy.
func HashFile(path string) (Hash, int64, error) {
	f, err := openRead(path)
	if err != nil {
		return Hash{}, 0, err
	}
	defer f.Close()
	return HashReader(f)
}

// openRead открывает файл на чтение, пережидая короткую занятость.
func openRead(path string) (*os.File, error) {
	var f *os.File
	err := withRetry(path, func() (err error) {
		f, err = os.Open(path)
		return err
	})
	return f, err
}
