package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// Ссылка на папку ставится, читается и снимается, а папка, на которую она
// вела, остаётся целой.
func TestLinkDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "project", "mod")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "mod.mod"), []byte("return {}"), 0o644)
	link := filepath.Join(dir, "game", "mod")
	os.MkdirAll(filepath.Dir(link), 0o755)

	if err := LinkDir(target, link); err != nil {
		t.Fatal(err)
	}
	if got, ok := LinkTarget(link); !ok || !SamePath(got, target) {
		t.Errorf("ссылка ведёт в %q (%v)", got, ok)
	}
	if data, err := os.ReadFile(filepath.Join(link, "mod.mod")); err != nil || string(data) != "return {}" {
		t.Errorf("через ссылку: %q, %v", data, err)
	}
	if _, ok := LinkTarget(target); ok {
		t.Error("обычная папка принята за ссылку")
	}
	if err := RemoveLink(target); err == nil {
		t.Error("обычная папка удалена как ссылка")
	}
	if err := RemoveLink(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("ссылка осталась: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "mod.mod")); err != nil {
		t.Errorf("папка проекта пострадала: %v", err)
	}
}
