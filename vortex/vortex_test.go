package vortex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		source, name, version string
		id                    int
	}{
		{"AFK-33-23-4-05-1680675716", "AFK", "23.4.05", 33},
		{"A La Mode 3.4.0 663 3.4.0 2026-08-23T13-44Z VWLqrWBmn", "A La Mode", "3.4.0", 663},
		{"Alfs DMF Extensions 864 2.0.7 2026-09-19T21-09Z VWLqrWBYa", "Alfs DMF Extensions", "2.0.7", 864},
		{"Auto Mod Loading and Ordering 246 2026-07-06 2026-07-06T05-25Z Z1K0I1fnl", "Auto Mod Loading and Ordering", "2026-07-06", 246},
		{"Darktide Mod Loader 19 26.06.24 2026-06-24T06-07Z ndQ1md9gG", "Darktide Mod Loader", "26.06.24", 19},
		{"BarrelWarning.zip-376-V1-0-1728547533", "BarrelWarning", "V1.0", 376},
		{"better_hud_info_view-511-1-5-1-1753031618", "better hud info view", "1.5.1", 511},
		{"Мой мод", "Мой мод", "", 0},
	}
	for _, tt := range tests {
		got := ParseSource(tt.source)
		if got.Name != tt.name || got.Version != tt.version || got.NexusID != tt.id || !got.AsIs || got.Source != "Vortex" {
			t.Errorf("ParseSource(%q) = %+v; want %q %q %d", tt.source, got, tt.name, tt.version, tt.id)
		}
	}
}

func TestParseDeployment(t *testing.T) {
	d, err := ParseDeployment([]byte(`{"version":1,"instance":"abc","deploymentMethod":"hardlink_activator","files":[
		{"relPath":"mods\\afk\\afk.mod","source":"AFK-33-23-4-05-1680675716","target":"","time":1},
		{"relPath":"mods\\afk\\scripts\\afk.lua","source":"AFK-33-23-4-05-1680675716"},
		{"relPath":"bundle\\9ba626afa44a3aa3.patch_999","source":"DML"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Instance != "abc" || len(d.Files) != 3 || d.Files[1].RelPath != "mods/afk/scripts/afk.lua" {
		t.Errorf("учёт = %+v", d)
	}
	if s := d.Sources(); len(s) != 2 || s[1] != "DML" {
		t.Errorf("моды = %v", s)
	}
	for _, bad := range []string{`{`, `{"files":[{"relPath":"","source":"x"}]}`} {
		if _, err := ParseDeployment([]byte(bad)); err == nil {
			t.Errorf("принят испорченный учёт %s", bad)
		}
	}
}

func TestFindStaging(t *testing.T) {
	base := t.TempDir()
	game := filepath.Join(base, "Игра")
	staging := filepath.Join(base, "warhammer40kdarktide")
	src := filepath.Join(staging, "AFK-33", "mods", "afk", "afk.mod")
	os.MkdirAll(filepath.Dir(src), 0o755)
	os.WriteFile(src, []byte("return {}"), 0o644)
	os.WriteFile(filepath.Join(staging, stagingMarker), []byte(`{"instance":"abc"}`), 0o644)
	dst := filepath.Join(game, "mods", "afk", "afk.mod")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Link(src, dst); err != nil {
		t.Skipf("жёсткие ссылки недоступны: %v", err)
	}

	d := &Deployment{Files: []File{{RelPath: "mods/afk/afk.mod", Source: "AFK-33"}}}
	got, err := FindStaging(game, d)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(got, staging) {
		t.Errorf("хранилище = %q, want %q", got, staging)
	}

	// Без маркера Vortex папка хранилищем не считается.
	os.Remove(filepath.Join(staging, stagingMarker))
	if _, err := FindStaging(game, d); err == nil {
		t.Error("папка без маркера принята за хранилище Vortex")
	}
}

// fakeStaging makes a Vortex staging folder with one mod file and an empty
// game folder; it returns the game folder, the staging folder and the file.
func fakeStaging(t *testing.T) (game, staging, src string) {
	base := t.TempDir()
	game = filepath.Join(base, "Игра")
	staging = filepath.Join(base, "Vortex Mods", "warhammer40kdarktide")
	src = filepath.Join(staging, "AFK-33", "mods", "afk", "afk.mod")
	os.MkdirAll(filepath.Dir(src), 0o755)
	os.WriteFile(src, []byte("return {}"), 0o644)
	os.WriteFile(filepath.Join(staging, stagingMarker), []byte(`{"instance":"abc"}`), 0o644)
	os.MkdirAll(filepath.Join(game, "mods", "afk"), 0o755)
	return game, staging, src
}

// Vortex names the staging folder in its deployment file: it is found even
// when the game files are plain copies.
func TestFindStagingFromManifest(t *testing.T) {
	game, staging, src := fakeStaging(t)
	data, _ := os.ReadFile(src)
	os.WriteFile(filepath.Join(game, "mods", "afk", "afk.mod"), data, 0o644)
	d := &Deployment{Staging: staging, Files: []File{{RelPath: "mods/afk/afk.mod", Source: "AFK-33"}}}
	if got, err := FindStaging(game, d); err != nil || !sameDir(got, staging) {
		t.Errorf("хранилище из учёта = %q, %v", got, err)
	}
	// A path without the Vortex marker is not trusted.
	os.Remove(filepath.Join(staging, stagingMarker))
	if _, err := FindStaging(game, d); err == nil {
		t.Error("папка из учёта без маркера принята за хранилище Vortex")
	}
}

// Symlink Deployment: the game files are symlinks into the staging folder,
// which Vortex picks when that folder is on another drive.
func TestFindStagingSymlinks(t *testing.T) {
	game, staging, src := fakeStaging(t)
	if err := os.Symlink(src, filepath.Join(game, "mods", "afk", "afk.mod")); err != nil {
		t.Skipf("символические ссылки недоступны: %v", err)
	}
	d := &Deployment{Method: "symlink_activator", Files: []File{{RelPath: "mods/afk/afk.mod", Source: "AFK-33"}}}
	if got, err := FindStaging(game, d); err != nil || !sameDir(got, staging) {
		t.Errorf("хранилище по символической ссылке = %q, %v", got, err)
	}
}

func TestFindStagingMoved(t *testing.T) {
	game, staging, _ := fakeStaging(t)
	d := &Deployment{Method: moveMethod, Staging: staging, Files: []File{{RelPath: "mods/afk/afk.mod", Source: "AFK-33"}}}
	if _, err := FindStaging(game, d); err == nil || !strings.Contains(err.Error(), "Move Deployment") {
		t.Errorf("перенос файлов: %v", err)
	}
}

func sameDir(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

func TestIsMarker(t *testing.T) {
	for name, want := range map[string]bool{
		"__folder_managed_by_vortex": true, "mods/a/__folder_managed_by_vortex": true,
		"__vortex_staging_folder": true, "afk.mod": false, "vortex.lua": false,
	} {
		if IsMarker(name) != want {
			t.Errorf("IsMarker(%q) = %v", name, !want)
		}
	}
}

// Настоящая установка на этом компьютере, только чтение. Без Vortex — пропуск.
func TestRealVortex(t *testing.T) {
	game := os.Getenv("MODVAULT_REAL_GAME")
	if game == "" {
		game = `G:\SteamLibrary\steamapps\common\Warhammer 40,000 DARKTIDE`
	}
	d, err := ReadDeployment(game)
	if err != nil {
		t.Skipf("учёта Vortex нет: %v", err)
	}
	staging, err := FindStaging(game, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Vortex: %d файлов, %d модов, хранилище %s", len(d.Files), len(d.Sources()), staging)
	unparsed := 0
	for _, s := range d.Sources() {
		if ParseSource(s).Version == "" {
			unparsed++
			t.Logf("имя не разобрано: %s", s)
		}
	}
	if unparsed > len(d.Sources())/10 {
		t.Errorf("не разобрано %d имён из %d", unparsed, len(d.Sources()))
	}
}
