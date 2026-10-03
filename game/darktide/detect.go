package darktide

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zemidala/modvault/game"
)

var (
	vdfPath       = regexp.MustCompile(`"path"\s+"([^"]+)"`)
	acfInstallDir = regexp.MustCompile(`"installdir"\s+"([^"]+)"`)
)

// Detect ищет Darktide в библиотеках Steam и в папках Xbox.
func (d *Darktide) Detect() ([]game.Install, error) {
	var found []game.Install
	seen := map[string]bool{}
	add := func(dir, store string) {
		k := strings.ToLower(filepath.Clean(dir))
		if seen[k] || d.Validate(dir) != nil {
			return
		}
		seen[k] = true
		found = append(found, game.Install{Dir: filepath.Clean(dir), Store: store})
	}

	if steam := steamRoot(); steam != "" {
		for _, lib := range SteamLibraries(steam) {
			if dir := SteamInstallDir(lib); dir != "" {
				add(dir, "Steam")
			}
		}
	}
	for _, drive := range drives() {
		matches, _ := filepath.Glob(filepath.Join(drive, "XboxGames", "*Darktide*", "Content"))
		for _, dir := range matches {
			add(dir, "Xbox")
		}
	}
	return found, nil
}

// SteamLibraries читает список библиотек из steamapps/libraryfolders.vdf.
// Папка самого Steam — тоже библиотека.
func SteamLibraries(steam string) []string {
	libs := []string{filepath.Clean(steam)}
	data, err := os.ReadFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return libs
	}
	for _, m := range vdfPath.FindAllStringSubmatch(string(data), -1) {
		libs = append(libs, filepath.Clean(strings.ReplaceAll(m[1], `\\`, `\`)))
	}
	return libs
}

// SteamInstallDir возвращает папку Darktide в библиотеке Steam или пусто.
func SteamInstallDir(library string) string {
	data, err := os.ReadFile(filepath.Join(library, "steamapps", "appmanifest_"+SteamAppID+".acf"))
	if err != nil {
		return ""
	}
	m := acfInstallDir.FindStringSubmatch(string(data))
	if m == nil {
		return ""
	}
	return filepath.Join(library, "steamapps", "common", m[1])
}
