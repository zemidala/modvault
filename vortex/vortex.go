// Package vortex читает то, что Vortex оставляет в папке игры, — чтобы
// Modvault мог принять его моды без переустановки и вернуть их обратно.
package vortex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/zemidala/modvault/core/store"
)

// DeploymentFile — файл учёта Vortex в корне папки игры.
const DeploymentFile = "vortex.deployment.json"

// Маркеры Vortex: в папке-хранилище и в папках игры.
const (
	stagingMarker = "__vortex_staging_folder"
	folderMarker  = "__folder_managed_by_vortex"
)

// Deployment — что Vortex развернул в игру.
type Deployment struct {
	Instance string `json:"instance"`
	Method   string `json:"deploymentMethod"`
	Files    []File `json:"files"`
}

// File — развёрнутый файл: путь в игре и папка мода в хранилище Vortex.
type File struct {
	RelPath string `json:"relPath"`
	Source  string `json:"source"`
}

// ReadDeployment читает учёт Vortex из папки игры.
func ReadDeployment(gameDir string) (*Deployment, error) {
	data, err := os.ReadFile(filepath.Join(gameDir, DeploymentFile))
	if err != nil {
		return nil, err
	}
	return ParseDeployment(data)
}

// ParseDeployment разбирает содержимое vortex.deployment.json.
func ParseDeployment(data []byte) (*Deployment, error) {
	var d Deployment
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", DeploymentFile, err)
	}
	for i, f := range d.Files {
		if f.RelPath == "" || f.Source == "" {
			return nil, fmt.Errorf("%s: запись без пути или мода", DeploymentFile)
		}
		d.Files[i].RelPath = strings.ReplaceAll(f.RelPath, `\`, "/")
	}
	return &d, nil
}

// Sources перечисляет моды, файлы которых развёрнуты, в порядке первого появления.
func (d *Deployment) Sources() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range d.Files {
		if !seen[f.Source] {
			seen[f.Source] = true
			out = append(out, f.Source)
		}
	}
	return out
}

// FindStaging находит папку-хранилище Vortex: файлы в игре — жёсткие ссылки
// на её файлы, так что достаточно найти другое имя одного из них.
func FindStaging(gameDir string, d *Deployment) (string, error) {
	for _, f := range d.Files {
		game := filepath.Join(gameDir, filepath.FromSlash(f.RelPath))
		names, err := hardLinks(game)
		if err != nil {
			continue
		}
		suffix := strings.ToLower(string(filepath.Separator) + f.Source + string(filepath.Separator) + filepath.FromSlash(f.RelPath))
		for _, name := range names {
			if strings.HasSuffix(strings.ToLower(name), suffix) {
				staging := name[:len(name)-len(suffix)]
				if IsStaging(staging) {
					return staging, nil
				}
			}
		}
	}
	return "", errors.New("не найдена папка, где Vortex хранит моды: файлы в игре не ссылаются на неё")
}

// IsStaging сообщает, что папка помечена Vortex как хранилище модов.
func IsStaging(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, stagingMarker))
	return err == nil
}

// IsMarker сообщает, что файл — служебная пометка Vortex, а не часть мода.
func IsMarker(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return strings.HasPrefix(base, "__vortex") || base == folderMarker
}

var (
	// «AFK-33-23-4-05-1680675716» — имя файла, скачанного с Nexus.
	nexusFileName = regexp.MustCompile(`^(.+?)-(\d+)-([0-9A-Za-z]+(?:-[0-9A-Za-z]+)*)-(\d{9,11})$`)
	// «Alfs DMF Extensions 864 2.0.7 2026-09-19T21-09Z VWLqrWBYa» — имя папки,
	// которое Vortex даёт модам, скачанным новым способом.
	vortexName = regexp.MustCompile(`^(.+?) (\d+) (\S+) (\d{4}-\d{2}-\d{2}T\d{2}-\d{2}Z) \S+$`)
)

// ParseSource угадывает название, версию и номер на Nexus по имени папки мода
// в хранилище Vortex.
func ParseSource(source string) store.Info {
	info := store.Info{Name: source, Source: "Vortex", AsIs: true}
	if m := vortexName.FindStringSubmatch(source); m != nil {
		info.Name = strings.TrimSpace(strings.TrimSuffix(m[1], " "+m[3]))
		info.NexusID, _ = strconv.Atoi(m[2])
		info.Version = m[3]
		return info
	}
	if m := nexusFileName.FindStringSubmatch(source); m != nil {
		name := m[1]
		for _, ext := range []string{".zip", ".7z", ".rar"} {
			name = strings.TrimSuffix(name, ext)
		}
		info.Name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
		info.NexusID, _ = strconv.Atoi(m[2])
		info.Version = strings.ReplaceAll(m[3], "-", ".")
	}
	return info
}
