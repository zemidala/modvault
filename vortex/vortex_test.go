package vortex

import (
	"os"
	"path/filepath"
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
