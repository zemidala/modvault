// Package deploy — развёртывание модов из хранилища в папку игры: расчёт
// плана, применение с журналом, откат после сбоя.
//
// Ядро не знает, куда игра ждёт файлы: путь файла в игре задаёт тот, кто
// собирает Source (на этапе 4 — плагин игры).
package deploy

import (
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/fsx"
)

// File — файл мода и путь, по которому он должен лечь в игре.
type File struct {
	Path string // в игре, относительно её корня, через «/»
	Src  string // полный путь к файлу в хранилище
	Hash fsx.Hash
	Size int64
}

// Source — включённый мод. Порядок Source задаёт порядок загрузки.
type Source struct {
	ModID     string
	VersionID string
	Files     []File
}

// Target — файл, который должен лежать в игре.
type Target struct {
	File
	ModID     string
	VersionID string
}

// Conflict — несколько модов кладут файл по одному пути.
type Conflict struct {
	Path   string
	Mods   []string // в порядке загрузки
	Winner string
}

// key сравнивает пути так же, как Windows: без учёта регистра.
func key(path string) string {
	return strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
}

// Desired собирает файлы, которые должны лежать в игре. В конфликте
// побеждает мод, стоящий ниже в порядке, если winners (путь → мод)
// не закрепляет другого из претендентов.
func Desired(sources []Source, winners map[string]string) (map[string]Target, []Conflict) {
	pinned := make(map[string]string, len(winners))
	for path, mod := range winners {
		pinned[key(path)] = mod
	}

	candidates := map[string][]Target{}
	for _, s := range sources {
		for _, f := range s.Files {
			f.Path = strings.ReplaceAll(f.Path, `\`, "/")
			k := key(f.Path)
			candidates[k] = append(candidates[k], Target{File: f, ModID: s.ModID, VersionID: s.VersionID})
		}
	}

	out := make(map[string]Target, len(candidates))
	var conflicts []Conflict
	for k, list := range candidates {
		winner := list[len(list)-1]
		for _, t := range list {
			if t.ModID == pinned[k] {
				winner = t
			}
		}
		out[k] = winner
		if len(list) > 1 {
			mods := make([]string, len(list))
			for i, t := range list {
				mods[i] = t.ModID
			}
			conflicts = append(conflicts, Conflict{Path: winner.Path, Mods: mods, Winner: winner.ModID})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return key(conflicts[i].Path) < key(conflicts[j].Path) })
	return out, conflicts
}
