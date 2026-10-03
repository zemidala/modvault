package darktide

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/zemidala/modvault/game"
)

// ErrLayout — архив не похож ни на одну известную раскладку мода.
var ErrLayout = errors.New("не удалось понять, как разложить архив")

// Папки и файлы корня игры: если они есть в архиве, архив повторяет корень.
var rootMarkers = map[string]bool{
	"mods": true, "binaries": true, "bundle": true, "tools": true, "launcher": true,
	"toggle_darktide_mods.bat": true,
}

// Описания и картинки рядом с модом в игру не идут.
var docExt = map[string]bool{
	".txt": true, ".md": true, ".pdf": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".url": true, ".html": true, ".htm": true, ".rtf": true,
}

func isDoc(p string) bool {
	return docExt[strings.ToLower(path.Ext(p))]
}

// Layout раскладывает архив мода. Поддерживаются:
//   - обычный мод: Мод/Мод.mod и его файлы → mods/Мод/…;
//   - .mod прямо в корне архива → mods/<имя .mod>/…;
//   - несколько модов в одном архиве;
//   - архив, повторяющий корень игры (mods/, binaries/, bundle/ …), — «оверлей»;
//   - папки-обёртки вокруг всего этого, в том числе с именами вида Мод-222-1-4-5-…;
//   - папка, названная не так, как .mod: в игре она получит имя .mod.
func Layout(files []string) (game.Layout, error) {
	clean := make([]string, 0, len(files))
	for _, f := range files {
		clean = append(clean, strings.Trim(strings.ReplaceAll(f, `\`, "/"), "/"))
	}
	if len(clean) == 0 {
		return game.Layout{}, fmt.Errorf("%w: архив пуст", ErrLayout)
	}

	prefix := ""
	for {
		rel := strip(clean, prefix)
		top := tops(rel)
		if hasRootMarker(top) {
			return overlay(clean, prefix)
		}
		if l, ok, err := modFolders(clean, prefix); ok || err != nil {
			return l, err
		}
		// Обёртка: всё лежит в одной папке, и в ней самой нет .mod.
		if len(top) == 1 && top[0].dir {
			prefix += top[0].name + "/"
			continue
		}
		return game.Layout{}, fmt.Errorf("%w: в архиве не найден файл .mod", ErrLayout)
	}
}

type entry struct {
	name string
	dir  bool
}

// strip возвращает пути, лежащие под prefix, без него.
func strip(files []string, prefix string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			out = append(out, strings.TrimPrefix(f, prefix))
		}
	}
	return out
}

// tops перечисляет записи верхнего уровня, не считая описаний.
func tops(files []string) []entry {
	seen := map[string]bool{}
	var out []entry
	for _, f := range files {
		name, rest, isDir := strings.Cut(f, "/")
		if !isDir && isDoc(name) {
			continue
		}
		k := strings.ToLower(name)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, entry{name: name, dir: isDir && rest != ""})
	}
	return out
}

func hasRootMarker(top []entry) bool {
	for _, e := range top {
		if rootMarkers[strings.ToLower(e.name)] {
			return true
		}
	}
	return false
}

// overlay кладёт архив в корень игры как есть.
func overlay(files []string, prefix string) (game.Layout, error) {
	l := game.Layout{Kind: "архив повторяет корень игры", Paths: map[string]string{}}
	folders := map[string]bool{}
	for _, f := range files {
		if !strings.HasPrefix(f, prefix) {
			l.Ignored = append(l.Ignored, f)
			continue
		}
		rel := strings.TrimPrefix(f, prefix)
		top, rest, _ := strings.Cut(rel, "/")
		if !rootMarkers[strings.ToLower(top)] && isDoc(rel) {
			l.Ignored = append(l.Ignored, f)
			continue
		}
		// Регистр корневых папок приводим к принятому в игре.
		if rootMarkers[strings.ToLower(top)] && rest != "" {
			rel = strings.ToLower(top) + "/" + rest
		}
		l.Paths[f] = rel
		if mod, ok := modFile(rel); ok {
			folders[mod] = true
		}
	}
	for f := range folders {
		l.Folders = append(l.Folders, f)
	}
	sort.Strings(l.Folders)
	l.Role = role(l)
	return l, nil
}

// modFile узнаёт путь mods/<папка>/<папка>.mod и возвращает имя папки.
func modFile(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "mods") || !strings.EqualFold(path.Ext(parts[2]), ".mod") {
		return "", false
	}
	return parts[1], true
}

// modFolders ищет моды под prefix: папки, в которых лежит .mod, или .mod прямо
// в корне. ok = false — модов на этом уровне нет, можно снимать обёртку.
func modFolders(files []string, prefix string) (game.Layout, bool, error) {
	rel := strip(files, prefix)

	// .mod прямо на этом уровне: весь уровень — один мод.
	var rootMods []string
	for _, f := range rel {
		if !strings.Contains(f, "/") && strings.EqualFold(path.Ext(f), ".mod") {
			rootMods = append(rootMods, f)
		}
	}
	if len(rootMods) > 1 {
		return game.Layout{}, false, fmt.Errorf("%w: в одной папке несколько файлов .mod: %s", ErrLayout, strings.Join(rootMods, ", "))
	}
	if len(rootMods) == 1 {
		name := strings.TrimSuffix(rootMods[0], path.Ext(rootMods[0]))
		l := game.Layout{Kind: "обычный мод", Paths: map[string]string{}, Folders: []string{name}}
		for _, f := range files {
			r := strings.TrimPrefix(f, prefix)
			if !strings.HasPrefix(f, prefix) || (!strings.Contains(r, "/") && isDoc(r)) {
				l.Ignored = append(l.Ignored, f)
				continue
			}
			l.Paths[f] = "mods/" + name + "/" + r
		}
		l.Role = role(l)
		return l, true, nil
	}

	// Папки верхнего уровня, в каждой из которых лежит свой .mod.
	modName := map[string]string{} // папка в архиве → имя .mod
	for _, f := range rel {
		parts := strings.Split(f, "/")
		if len(parts) == 2 && strings.EqualFold(path.Ext(parts[1]), ".mod") {
			if prev, dup := modName[parts[0]]; dup {
				return game.Layout{}, false, fmt.Errorf("%w: в папке %s несколько файлов .mod: %s, %s", ErrLayout, parts[0], prev, parts[1])
			}
			modName[parts[0]] = strings.TrimSuffix(parts[1], path.Ext(parts[1]))
		}
	}
	if len(modName) == 0 {
		return game.Layout{}, false, nil
	}

	l := game.Layout{Kind: "обычный мод", Paths: map[string]string{}}
	if len(modName) > 1 {
		l.Kind = "несколько модов в одном архиве"
	}
	for _, f := range files {
		r := strings.TrimPrefix(f, prefix)
		dir, rest, isDir := strings.Cut(r, "/")
		if !strings.HasPrefix(f, prefix) || !isDir {
			if isDoc(r) || !strings.HasPrefix(f, prefix) {
				l.Ignored = append(l.Ignored, f)
				continue
			}
			return game.Layout{}, false, fmt.Errorf("%w: файл %s лежит вне папки мода", ErrLayout, r)
		}
		name, ok := modName[dir]
		if !ok {
			if isDoc(r) {
				l.Ignored = append(l.Ignored, f)
				continue
			}
			return game.Layout{}, false, fmt.Errorf("%w: в папке %s нет файла .mod", ErrLayout, dir)
		}
		if name != dir && l.Kind == "обычный мод" {
			l.Kind = "обычный мод, папка переименована по файлу .mod"
		}
		l.Paths[f] = "mods/" + name + "/" + rest
	}
	for _, name := range modName {
		l.Folders = append(l.Folders, name)
	}
	sort.Strings(l.Folders)
	l.Role = role(l)
	return l, true, nil
}

// Папки загрузчика и фреймворка; в порядок загрузки они не пишутся.
const (
	loaderFolder    = "base"
	frameworkFolder = "dmf"
)

func role(l game.Layout) game.Role {
	for _, dst := range l.Paths {
		if strings.EqualFold(dst, "mods/base/base.mod") || strings.HasPrefix(strings.ToLower(dst), "binaries/mod_loader") {
			return game.RoleLoader
		}
	}
	for _, f := range l.Folders {
		if strings.EqualFold(f, frameworkFolder) {
			return game.RoleFramework
		}
	}
	return game.RoleNone
}
