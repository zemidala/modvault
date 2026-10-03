package store

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Имя файла, скачанного с Nexus: «Название-<номер мода>-<версия через дефисы>-<время>»,
// например «Scoreboard-22-1-4-0-1700000000.zip».
var nexusName = regexp.MustCompile(`^(.+?)-(\d+)-([0-9A-Za-z]+(?:-[0-9A-Za-z]+)*)-(\d{9,11})$`)

// GuessInfo угадывает название и версию мода по имени архива. Если имя
// не похоже на файл с Nexus, названием становится имя файла без расширения,
// а версия остаётся пустой.
func GuessInfo(archivePath string) Info {
	base := filepath.Base(archivePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	if m := nexusName.FindStringSubmatch(base); m != nil {
		id, _ := strconv.Atoi(m[2])
		return Info{
			Name:    strings.TrimSpace(strings.ReplaceAll(m[1], "_", " ")),
			Version: strings.ReplaceAll(m[3], "-", "."),
			Source:  "Nexus Mods",
			NexusID: id,
		}
	}
	return Info{Name: strings.TrimSpace(base), Source: "Архив с диска"}
}
