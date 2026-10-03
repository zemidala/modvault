// Package darktide — плагин Warhammer 40,000: Darktide.
package darktide

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/i18n"
)

// SteamAppID — номер игры в Steam.
const SteamAppID = "1361210"

const (
	loadOrderPath = "mods/mod_load_order.txt"
	exePath       = "binaries/Darktide.exe"
)

// Darktide реализует game.Game.
type Darktide struct {
	// Patcher патчит bundle_database.data в папке dir; nil — dtkit-patch из игры.
	Patcher func(gameDir, dir string) error
	// LogDir — папка журналов игры; пусто — журналы не читаются.
	LogDir string

	mu      sync.Mutex
	lastRun *lastRunCache
}

func New() *Darktide { return &Darktide{} }

func (*Darktide) ID() string   { return "darktide" }
func (*Darktide) Name() string { return "Darktide" }

// NexusDomain — имя игры в адресах Nexus Mods.
func (*Darktide) NexusDomain() string { return "warhammer40kdarktide" }

func (*Darktide) Layout(files []string) (game.Layout, error) { return Layout(files) }

func (*Darktide) Describe(files []string) game.Layout { return Describe(files) }

// Originals находит оригинал базы бандлов, который dtkit-patch оставляет
// рядом с пропатченной: bundle_database.data.bak.
func (*Darktide) Originals(dir string) map[string]string {
	bak := filepath.Join(dir, filepath.FromSlash(bundleDBPath)+".bak")
	data, err := os.ReadFile(bak)
	if err != nil || InspectBundle(data) != Unpatched {
		return nil
	}
	return map[string]string{bundleDBPath: bak}
}

// Validate проверяет, что в папке есть исполняемый файл и база бандлов игры.
func (*Darktide) Validate(dir string) error {
	for _, rel := range []string{exePath, bundleDBPath} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return i18n.Errorf("в папке нет %s — похоже, это не папка Darktide", rel)
		}
	}
	return nil
}

// Managers ищет следы других менеджеров модов. Учитывается только Vortex:
// он ведёт учёт развёрнутых файлов прямо в папке игры.
func (*Darktide) Managers(dir string) []string {
	if _, err := os.Stat(filepath.Join(dir, "vortex.deployment.json")); err == nil {
		return []string{"Vortex"}
	}
	return nil
}

// Generate собирает mod_load_order.txt и пропатченную базу бандлов.
func (d *Darktide) Generate(ctx game.Context) ([]game.Generated, []game.Notice, error) {
	if err := os.MkdirAll(ctx.WorkDir, 0o755); err != nil {
		return nil, nil, err
	}
	var out []game.Generated
	var notices []game.Notice

	loader, framework := false, false
	for _, m := range ctx.Mods {
		if !m.Enabled {
			continue
		}
		loader = loader || m.Layout.Role == game.RoleLoader
		framework = framework || m.Layout.Role == game.RoleFramework
	}
	hasMods := false
	for _, m := range ctx.Mods {
		hasMods = hasMods || (m.Enabled && m.Layout.Role == game.RoleNone)
	}
	if hasMods && !loader {
		notices = append(notices, game.Notice{Level: game.Error, Title: i18n.T("Не установлен Darktide Mod Loader"),
			Detail: i18n.T("Без него игра не загрузит моды. Скачайте DML с Nexus и добавьте как обычный мод")})
	}
	if hasMods && !framework {
		notices = append(notices, game.Notice{Level: game.Warn, Title: i18n.T("Не установлен Darktide Mod Framework"),
			Detail: i18n.T("Большинству модов нужен DMF. Скачайте его с Nexus и добавьте как обычный мод")})
	}
	if !loader {
		// Без загрузчика нет ни порядка загрузки, ни патча.
		return nil, notices, nil
	}

	src, err := writeGenerated(ctx.WorkDir, "mod_load_order", ".txt", LoadOrder(ctx.Mods))
	if err != nil {
		return nil, notices, err
	}
	out = append(out, game.Generated{Path: loadOrderPath, Src: src})

	patched, notice, err := d.bundle(ctx)
	if err != nil {
		return nil, notices, err
	}
	if notice != nil {
		notices = append(notices, *notice)
	}
	if patched != "" {
		out = append(out, game.Generated{Path: bundleDBPath, Src: patched})
	}
	return out, notices, nil
}

// LoadOrder собирает mod_load_order.txt: папка мода на строку, выключенный
// мод — строкой «-- имя». Загрузчик и фреймворк DML грузит сам.
func LoadOrder(mods []game.ModInfo) []byte {
	var b bytes.Buffer
	// Строка идёт в файл игры и от языка окна не зависит: иначе смена языка
	// выглядела бы как изменение, ждущее развёртывания.
	b.WriteString("-- Файл собран Modvault. Правки вручную пропадут при следующем развёртывании.\r\n")
	seen := map[string]bool{}
	for _, m := range mods {
		for _, folder := range m.Layout.Folders {
			k := strings.ToLower(folder)
			if k == loaderFolder || k == frameworkFolder || seen[k] {
				continue
			}
			seen[k] = true
			if !m.Enabled {
				b.WriteString("-- ")
			}
			b.WriteString(folder)
			b.WriteString("\r\n")
		}
	}
	return b.Bytes()
}

// Launch запускает игру. Через Steam — чтобы работали оверлей и облако.
func (*Darktide) Launch(inst game.Install) error {
	if inst.Store != "Steam" {
		return i18n.NewError("запуск этой версии игры пока не поддерживается: запустите её из приложения Xbox")
	}
	if inst.ViaLauncher {
		return openURL("steam://rungameid/" + SteamAppID) // Steam сам откроет лаунчер игры
	}
	// Игра стартует сама, минуя окно лаунчера. Без Steam она не войдёт в
	// учётную запись, поэтому запускать её без него бессмысленно.
	if !steamRunning() {
		return i18n.NewError("Steam не запущен: без него игра не стартует. Запустите Steam и нажмите «Играть» ещё раз")
	}
	cmd, err := launchCommand(inst.Dir)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return i18n.Errorf("игра не запустилась: %w", err)
	}
	return cmd.Process.Release() // игра живёт сама по себе, ждать её не нужно
}

// launchArgs — параметры, с которыми игру запускает её собственный лаунчер.
// Взяты из настроек лаунчера (launcher/Launcher.exe.config, ExeArgs) и из
// строки параметров настоящего запуска в журнале игры.
var launchArgs = []string{"-eac-untrusted", "--bundle-dir", "../bundle", "--ini", "settings", "--lua-heap-mb-size", "2048"}

// launchCommand собирает запуск Darktide.exe напрямую, без лаунчера.
func launchCommand(dir string) (*exec.Cmd, error) {
	exe := filepath.Join(dir, filepath.FromSlash(exePath))
	if _, err := os.Stat(exe); err != nil {
		return nil, i18n.Errorf("в папке игры нет %s", exePath)
	}
	cmd := exec.Command(exe, launchArgs...)
	cmd.Dir = filepath.Dir(exe) // пути в параметрах заданы от папки binaries
	// Лаунчер запускается самим Steam и получает номер игры от него; игре,
	// запущенной напрямую, номер нужно сообщить, иначе она не найдёт Steam.
	cmd.Env = append(os.Environ(), "SteamAppId="+SteamAppID, "SteamGameId="+SteamAppID)
	return cmd, nil
}
