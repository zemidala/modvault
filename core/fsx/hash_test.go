package fsx

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// SHA-256 строки «abc» из FIPS 180-2.
const abcHash = "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestHashReader(t *testing.T) {
	h, n, err := HashReader(strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("прочитано %d байт, want 3", n)
	}
	if h.String() != abcHash {
		t.Errorf("хеш = %s, want %s", h, abcHash)
	}
	if h.IsZero() {
		t.Error("IsZero для настоящего хеша")
	}
	if !(Hash{}).IsZero() {
		t.Error("IsZero для пустого хеша = false")
	}
}

func TestHashFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := WriteFile(path, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	h, n, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || h.String() != abcHash {
		t.Errorf("HashFile = %s, %d", h, n)
	}

	if _, _, err := HashFile(path + ".нет"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("несуществующий файл: %v, want fs.ErrNotExist", err)
	}
}

func TestParseHash(t *testing.T) {
	h, err := ParseHash(abcHash)
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != abcHash {
		t.Errorf("разбор и обратно = %s", h)
	}

	bad := []string{
		"",
		"sha256:",
		"sha256:ba78",
		strings.TrimPrefix(abcHash, "sha256:"),
		"md5:" + strings.TrimPrefix(abcHash, "sha256:"),
		"sha256:" + strings.Repeat("z", 64),
		abcHash + "00",
	}
	for _, s := range bad {
		if _, err := ParseHash(s); err == nil {
			t.Errorf("ParseHash(%q) прошёл без ошибки", s)
		}
	}
}

func TestHashJSON(t *testing.T) {
	type record struct {
		Hash Hash `json:"hash"`
	}
	h, _ := ParseHash(abcHash)

	data, err := json.Marshal(record{h})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"hash":"` + abcHash + `"}`; string(data) != want {
		t.Errorf("JSON = %s, want %s", data, want)
	}

	var back record
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Hash != h {
		t.Errorf("после JSON хеш = %s", back.Hash)
	}
	if err := json.Unmarshal([]byte(`{"hash":"мусор"}`), &back); err == nil {
		t.Error("неверный хеш в JSON прошёл без ошибки")
	}
}
