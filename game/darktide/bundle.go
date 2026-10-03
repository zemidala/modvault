package darktide

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/i18n"
)

const (
	bundleDBPath = "bundle/bundle_database.data"
	dtkitPath    = "tools/dtkit-patch.exe"
	autopatchDLL = "binaries/plugins/_dt_mod_autopatch.dll"
)

var (
	// Дескриптор boot-бандла, после которого патч вставляет свою запись.
	bundleAnchor = []byte{0xa3, 0x3a, 0x4a, 0xa4, 0xaf, 0x26, 0xa6, 0x9b}
	// Следы патчей: dtkit-patch и автопатчер DML.
	markerDtkit     = []byte("9ba626afa44a3aa3.patch_999")
	markerAutopatch = []byte("9ba626afa44a3aa3.patch_001")
)

// PatchState — что найдено в базе бандлов.
type PatchState int

const (
	Unpatched     PatchState = iota
	PatchedDtkit             // уже пропатчена dtkit-patch
	PatchedAuto              // уже пропатчена автопатчером DML
	UnknownFormat            // якоря нет: формат изменился после обновления игры
)

// InspectBundle разбирает содержимое bundle_database.data.
func InspectBundle(data []byte) PatchState {
	switch {
	case bytes.Contains(data, markerAutopatch):
		return PatchedAuto
	case bytes.Contains(data, markerDtkit):
		return PatchedDtkit
	case !bytes.Contains(data, bundleAnchor):
		return UnknownFormat
	}
	return Unpatched
}

// bundle возвращает путь к пропатченной копии оригинальной базы бандлов
// или пусто, если класть её не нужно.
func (d *Darktide) bundle(ctx game.Context) (string, *game.Notice, error) {
	if autopatch(ctx) {
		return "", &game.Notice{Level: game.Info, Title: i18n.T("Базу бандлов патчит автопатчер DML"),
			Detail: i18n.T("Modvault её не трогает")}, nil
	}

	original, err := ctx.Original(bundleDBPath)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(original)
	if err != nil {
		return "", nil, err
	}
	switch InspectBundle(data) {
	case PatchedDtkit, PatchedAuto:
		return "", &game.Notice{Level: game.Warn, Title: i18n.T("База бандлов уже пропатчена другим инструментом"),
			Detail: i18n.T("Modvault оставит её как есть. Чтобы патчем управлял Modvault, снимите чужой патч (toggle_darktide_mods.bat)")}, nil
	case UnknownFormat:
		return "", &game.Notice{Level: game.Error, Title: i18n.T("Не удалось пропатчить базу бандлов"),
			Detail: i18n.T("В ней нет ожидаемой записи — вероятно, игра обновилась и формат изменился. Моды не загрузятся, пока DML не выпустит обновление")}, nil
	}

	// Пропатченная копия зависит только от оригинала: собираем её один раз.
	sum := sha256.Sum256(data)
	out := filepath.Join(ctx.WorkDir, "bundle_database-"+hex.EncodeToString(sum[:8])+".data")
	if _, err := os.Stat(out); err == nil {
		return out, nil, nil
	}

	work, err := os.MkdirTemp(ctx.WorkDir, "patch-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(work)
	target := filepath.Join(work, "bundle_database.data")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", nil, err
	}
	patch := d.Patcher
	if patch == nil {
		patch = dtkitPatch
	}
	if err := patch(ctx.Dir, work); err != nil {
		return "", &game.Notice{Level: game.Error, Title: i18n.T("Не удалось пропатчить базу бандлов"), Detail: err.Error()}, nil
	}
	result, err := os.ReadFile(target)
	if err != nil {
		return "", nil, err
	}
	if InspectBundle(result) != PatchedDtkit {
		return "", &game.Notice{Level: game.Error, Title: i18n.T("Не удалось пропатчить базу бандлов"),
			Detail: i18n.T("Патчер отработал, но патча в файле нет")}, nil
	}
	if err := fsx.WriteFile(out, result); err != nil {
		return "", nil, err
	}
	return out, nil, nil
}

// autopatch сообщает, что базу бандлов патчит сам DML: его автопатчер
// лежит в игре или ляжет вместе с модами.
func autopatch(ctx game.Context) bool {
	if _, err := os.Stat(filepath.Join(ctx.Dir, filepath.FromSlash(autopatchDLL))); err == nil {
		return true
	}
	for _, m := range ctx.Mods {
		if !m.Enabled {
			continue
		}
		for _, dst := range m.Layout.Paths {
			if strings.EqualFold(dst, autopatchDLL) {
				return true
			}
		}
	}
	return false
}

// dtkitPatch запускает dtkit-patch из папки игры на копии базы в dir.
func dtkitPatch(gameDir, dir string) error {
	exe := filepath.Join(gameDir, filepath.FromSlash(dtkitPath))
	if _, err := os.Stat(exe); err != nil {
		return i18n.NewError("в игре нет tools/dtkit-patch.exe: он приходит вместе с DML, разверните DML")
	}
	cmd := exec.Command(exe, "--patch", dir)
	cmd.SysProcAttr = hiddenWindow()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dtkit-patch: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeGenerated кладёт собранный файл в папку work под именем по хешу
// содержимого: одинаковое содержимое — один файл.
func writeGenerated(work, name, ext string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	path := filepath.Join(work, name+"-"+hex.EncodeToString(sum[:8])+ext)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return path, fsx.WriteFile(path, data)
}
