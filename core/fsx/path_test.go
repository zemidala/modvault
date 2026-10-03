package fsx

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSafeJoin(t *testing.T) {
	root := filepath.Join("root", "mods")

	good := []struct {
		rel  string
		want string
	}{
		{"a.txt", filepath.Join(root, "a.txt")},
		{"dir/a.txt", filepath.Join(root, "dir", "a.txt")},
		{`dir\sub\a.txt`, filepath.Join(root, "dir", "sub", "a.txt")},
		{"./dir//a.txt", filepath.Join(root, "dir", "a.txt")},
		{"dir/", filepath.Join(root, "dir")},
		{"Моды/Тёмный прилив/файл.lua", filepath.Join(root, "Моды", "Тёмный прилив", "файл.lua")},
		{"console.log", filepath.Join(root, "console.log")},
		{"..hidden", filepath.Join(root, "..hidden")},
	}
	for _, tt := range good {
		got, err := SafeJoin(root, tt.rel)
		if err != nil {
			t.Errorf("SafeJoin(%q): %v", tt.rel, err)
			continue
		}
		if got != tt.want {
			t.Errorf("SafeJoin(%q) = %q, want %q", tt.rel, got, tt.want)
		}
	}

	bad := []string{
		"",
		".",
		"/",
		"/etc/passwd",
		`\Windows\system32`,
		`\\server\share\a`,
		"C:/Windows",
		`C:\Windows`,
		"C:a.txt",
		"..",
		"../a.txt",
		"dir/../../a.txt",
		`dir\..\a.txt`,
		"a.txt:stream",
		"NUL",
		"nul.txt",
		"dir/COM1",
		"lpt9.log",
		"con .txt",
		"name.",
		"name ",
		"dir./a.txt",
		"a?.txt",
		"a*.txt",
		"a|b",
		"a\x00b",
		"a\nb",
	}
	for _, rel := range bad {
		got, err := SafeJoin(root, rel)
		if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("SafeJoin(%q) = %q, %v; want ErrUnsafePath", rel, got, err)
		}
	}
}
