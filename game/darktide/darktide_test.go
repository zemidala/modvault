package darktide

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zemidala/modvault/game"
)

func mapping(l game.Layout) string {
	var lines []string
	for src, dst := range l.Paths {
		lines = append(lines, src+" → "+dst)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestLayout(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		want    string
		folders string
		role    game.Role
	}{
		{
			name:    "обычный мод",
			files:   []string{"Scoreboard/Scoreboard.mod", "Scoreboard/scripts/mods/Scoreboard/Scoreboard.lua", "README.md"},
			want:    "Scoreboard/Scoreboard.mod → mods/Scoreboard/Scoreboard.mod\nScoreboard/scripts/mods/Scoreboard/Scoreboard.lua → mods/Scoreboard/scripts/mods/Scoreboard/Scoreboard.lua",
			folders: "Scoreboard",
		},
		{
			name:    "обёртка с именем как на Nexus",
			files:   []string{"Scoreboard-22-1-4-0-1700000000/Scoreboard/Scoreboard.mod", "Scoreboard-22-1-4-0-1700000000/Scoreboard/a.lua"},
			want:    "Scoreboard-22-1-4-0-1700000000/Scoreboard/Scoreboard.mod → mods/Scoreboard/Scoreboard.mod\nScoreboard-22-1-4-0-1700000000/Scoreboard/a.lua → mods/Scoreboard/a.lua",
			folders: "Scoreboard",
		},
		{
			name:    "папка не совпадает с .mod",
			files:   []string{"numeric-ui-main/NumericUI.mod", `numeric-ui-main\scripts\a.lua`},
			want:    "numeric-ui-main/NumericUI.mod → mods/NumericUI/NumericUI.mod\nnumeric-ui-main/scripts/a.lua → mods/NumericUI/scripts/a.lua",
			folders: "NumericUI",
		},
		{
			name:    ".mod в корне архива",
			files:   []string{"afk.mod", "scripts/mods/afk/afk.lua", "changelog.txt"},
			want:    "afk.mod → mods/afk/afk.mod\nscripts/mods/afk/afk.lua → mods/afk/scripts/mods/afk/afk.lua",
			folders: "afk",
		},
		{
			name:    "вложенная mods/",
			files:   []string{"mods/Flux/Flux.mod", "mods/Flux/f.lua"},
			want:    "mods/Flux/Flux.mod → mods/Flux/Flux.mod\nmods/Flux/f.lua → mods/Flux/f.lua",
			folders: "Flux",
		},
		{
			name:    "DML: оверлей в корень",
			files:   []string{"binaries/mod_loader", "mods/base/base.mod", "mods/base/mod_manager.lua", "tools/dtkit-patch.exe", "toggle_darktide_mods.bat", "README.md"},
			want:    "binaries/mod_loader → binaries/mod_loader\nmods/base/base.mod → mods/base/base.mod\nmods/base/mod_manager.lua → mods/base/mod_manager.lua\ntoggle_darktide_mods.bat → toggle_darktide_mods.bat\ntools/dtkit-patch.exe → tools/dtkit-patch.exe",
			folders: "base",
			role:    game.RoleLoader,
		},
		{
			name:    "DMF в обёртке",
			files:   []string{"Darktide Mod Framework/mods/dmf/dmf.mod", "Darktide Mod Framework/mods/dmf/scripts/dmf.lua"},
			want:    "Darktide Mod Framework/mods/dmf/dmf.mod → mods/dmf/dmf.mod\nDarktide Mod Framework/mods/dmf/scripts/dmf.lua → mods/dmf/scripts/dmf.lua",
			folders: "dmf",
			role:    game.RoleFramework,
		},
		{
			name:    "оверлей с ассетами (Custom Assets)",
			files:   []string{"Bundle/abc123.patch_001", "mods/custom_assets/custom_assets.mod"},
			want:    "Bundle/abc123.patch_001 → bundle/abc123.patch_001\nmods/custom_assets/custom_assets.mod → mods/custom_assets/custom_assets.mod",
			folders: "custom_assets",
		},
		{
			name:    "два мода в одном архиве",
			files:   []string{"A/A.mod", "A/a.lua", "B/B.mod"},
			want:    "A/A.mod → mods/A/A.mod\nA/a.lua → mods/A/a.lua\nB/B.mod → mods/B/B.mod",
			folders: "A,B",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := Layout(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if got := mapping(l); got != tt.want {
				t.Errorf("раскладка:\n%s\nwant:\n%s", got, tt.want)
			}
			if got := strings.Join(l.Folders, ","); got != tt.folders {
				t.Errorf("папки = %s, want %s", got, tt.folders)
			}
			if l.Role != tt.role {
				t.Errorf("роль = %q, want %q", l.Role, tt.role)
			}
		})
	}
}

func TestLayoutRejects(t *testing.T) {
	for name, files := range map[string][]string{
		"пусто":                {},
		"нет .mod":             {"scripts/a.lua", "b.lua"},
		"обёртка без .mod":     {"wrap/inner/a.lua"},
		"два .mod в корне":     {"a.mod", "b.mod"},
		"папка без .mod рядом": {"A/A.mod", "junk/x.lua"},
		"файл вне папки мода":  {"A/A.mod", "stray.lua"},
	} {
		if l, err := Layout(files); !errors.Is(err, ErrLayout) {
			t.Errorf("%s: %+v, %v; want ErrLayout", name, l, err)
		}
	}
}

func TestLoadOrder(t *testing.T) {
	mods := []game.ModInfo{
		{Enabled: true, Layout: game.Layout{Folders: []string{"base"}, Role: game.RoleLoader}},
		{Enabled: true, Layout: game.Layout{Folders: []string{"dmf"}, Role: game.RoleFramework}},
		{Enabled: true, Layout: game.Layout{Folders: []string{"Flux"}}},
		{Enabled: false, Layout: game.Layout{Folders: []string{"Scoreboard"}}},
		{Enabled: true, Layout: game.Layout{Folders: []string{"A", "B"}}},
		{Enabled: true, Layout: game.Layout{Folders: []string{"flux"}}},
	}
	got := string(LoadOrder(mods))
	lines := strings.Split(strings.TrimRight(got, "\r\n"), "\r\n")
	if !strings.HasPrefix(lines[0], "-- ") {
		t.Errorf("нет заголовка-комментария: %q", lines[0])
	}
	if want := "Flux|-- Scoreboard|A|B"; strings.Join(lines[1:], "|") != want {
		t.Errorf("порядок = %q, want %q", strings.Join(lines[1:], "|"), want)
	}
}

// fakeBundle — база бандлов с якорем; patched — с отметкой dtkit-patch.
func fakeBundle(patched bool) []byte {
	data := append(bytes.Repeat([]byte{1}, 100), bundleAnchor...)
	data = append(data, bytes.Repeat([]byte{2}, 100)...)
	if patched {
		data = append(data, markerDtkit...)
	}
	return data
}

func fakePatcher(calls *int) func(string, string) error {
	return func(_, dir string) error {
		*calls++
		f := filepath.Join(dir, "bundle_database.data")
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		return os.WriteFile(f, append(data, markerDtkit...), 0o644)
	}
}

func newGame(t *testing.T, bundle []byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range map[string][]byte{exePath: []byte("exe"), bundleDBPath: bundle} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	return dir
}

func ctxFor(t *testing.T, dir string, mods ...game.ModInfo) game.Context {
	return game.Context{
		Dir: dir, WorkDir: filepath.Join(t.TempDir(), "generated"), Mods: mods,
		Original: func(rel string) (string, error) { return filepath.Join(dir, filepath.FromSlash(rel)), nil },
	}
}

var (
	dml  = game.ModInfo{Enabled: true, Layout: game.Layout{Folders: []string{"base"}, Role: game.RoleLoader}}
	dmf  = game.ModInfo{Enabled: true, Layout: game.Layout{Folders: []string{"dmf"}, Role: game.RoleFramework}}
	flux = game.ModInfo{Enabled: true, Layout: game.Layout{Folders: []string{"Flux"}}}
)

func titles(n []game.Notice) string {
	var out []string
	for _, x := range n {
		out = append(out, x.Title)
	}
	return strings.Join(out, " | ")
}

func TestGenerate(t *testing.T) {
	dir := newGame(t, fakeBundle(false))
	calls := 0
	d := &Darktide{Patcher: fakePatcher(&calls)}
	ctx := ctxFor(t, dir, dml, dmf, flux)

	files, notices, err := d.Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Errorf("замечания: %s", titles(notices))
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Src
	}
	if len(got) != 2 || got[loadOrderPath] == "" || got[bundleDBPath] == "" {
		t.Fatalf("служебные файлы: %v", got)
	}
	if data, _ := os.ReadFile(got[bundleDBPath]); InspectBundle(data) != PatchedDtkit {
		t.Error("собранная база не пропатчена")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(bundleDBPath))); InspectBundle(data) != Unpatched {
		t.Error("Generate изменил базу в папке игры")
	}

	// Тот же оригинал — патчер второй раз не запускается.
	if _, _, err := d.Generate(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("патчер запускался %d раз, want 1", calls)
	}
}

func TestGenerateNotices(t *testing.T) {
	calls := 0
	d := &Darktide{Patcher: fakePatcher(&calls)}

	// Без загрузчика: ни порядка, ни патча, и громкое замечание.
	files, notices, err := d.Generate(ctxFor(t, newGame(t, fakeBundle(false)), flux))
	if err != nil || len(files) != 0 || !strings.Contains(titles(notices), "Mod Loader") || !strings.Contains(titles(notices), "Mod Framework") {
		t.Errorf("без DML: %v, %s, %v", files, titles(notices), err)
	}

	// Чужой патч: базу не трогаем.
	files, notices, _ = d.Generate(ctxFor(t, newGame(t, fakeBundle(true)), dml, dmf, flux))
	if len(files) != 1 || !strings.Contains(titles(notices), "уже пропатчена") {
		t.Errorf("чужой патч: %v, %s", files, titles(notices))
	}

	// Обновление игры сменило формат: патч не применяется.
	files, notices, _ = d.Generate(ctxFor(t, newGame(t, bytes.Repeat([]byte{7}, 300)), dml, dmf, flux))
	if len(files) != 1 || !strings.Contains(titles(notices), "Не удалось пропатчить") {
		t.Errorf("неизвестный формат: %v, %s", files, titles(notices))
	}

	// Автопатчер DML в комплекте мода: патчит он.
	auto := game.ModInfo{Enabled: true, Layout: game.Layout{Paths: map[string]string{"x": autopatchDLL}}}
	files, notices, _ = d.Generate(ctxFor(t, newGame(t, fakeBundle(false)), dml, dmf, auto))
	if len(files) != 1 || !strings.Contains(titles(notices), "автопатчер") {
		t.Errorf("автопатчер: %v, %s", files, titles(notices))
	}
	if calls != 0 {
		t.Errorf("патчер запускался %d раз, want 0", calls)
	}
}

func TestValidateAndManagers(t *testing.T) {
	d := New()
	dir := newGame(t, fakeBundle(false))
	if err := d.Validate(dir); err != nil {
		t.Errorf("Validate игры: %v", err)
	}
	if err := d.Validate(t.TempDir()); err == nil {
		t.Error("пустая папка принята за игру")
	}
	if m := d.Managers(dir); len(m) != 0 {
		t.Errorf("менеджеры в чистой игре: %v", m)
	}
	// Пустая папка с чужим названием — не менеджер модов.
	os.Mkdir(filepath.Join(dir, "Servo-Modquisitor-2"), 0o755)
	if m := d.Managers(dir); len(m) != 0 {
		t.Errorf("пустая папка принята за менеджер: %v", m)
	}
	os.WriteFile(filepath.Join(dir, "vortex.deployment.json"), []byte("{}"), 0o644)
	if m := strings.Join(d.Managers(dir), ","); m != "Vortex" {
		t.Errorf("менеджеры = %s", m)
	}
}

func TestSteamLibraries(t *testing.T) {
	steam := t.TempDir()
	lib := t.TempDir()
	os.MkdirAll(filepath.Join(steam, "steamapps"), 0o755)
	vdf := "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(steam, `\`, `\\`) + "\"\n\t}\n\t\"1\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(lib, `\`, `\\`) + "\"\n\t\t\"apps\"\n\t\t{\n\t\t\t\"1361210\"\t\t\"1\"\n\t\t}\n\t}\n}\n"
	os.WriteFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"), []byte(vdf), 0o644)
	os.MkdirAll(filepath.Join(lib, "steamapps"), 0o755)
	os.WriteFile(filepath.Join(lib, "steamapps", "appmanifest_1361210.acf"), []byte("\"AppState\"\n{\n\t\"appid\"\t\t\"1361210\"\n\t\"installdir\"\t\t\"Warhammer 40,000 DARKTIDE\"\n}\n"), 0o644)

	libs := SteamLibraries(steam)
	if len(libs) != 3 || libs[2] != filepath.Clean(lib) {
		t.Errorf("библиотеки = %v", libs)
	}
	if got := SteamInstallDir(lib); got != filepath.Join(lib, "steamapps", "common", "Warhammer 40,000 DARKTIDE") {
		t.Errorf("папка игры = %q", got)
	}
	if got := SteamInstallDir(steam); got != "" {
		t.Errorf("в библиотеке без игры нашлось %q", got)
	}
}

// Настоящий dtkit-patch на копии оригинальной базы с этого компьютера.
// Папку игры тест только читает. Без установленной игры пропускается.
func TestRealDtkitPatch(t *testing.T) {
	installs, _ := New().Detect()
	var dir string
	for _, inst := range installs {
		if _, err := os.Stat(filepath.Join(inst.Dir, filepath.FromSlash(dtkitPath))); err == nil {
			dir = inst.Dir
			break
		}
	}
	if dir == "" {
		t.Skip("Darktide с DML на этом компьютере не найден")
	}
	original := filepath.Join(dir, filepath.FromSlash(bundleDBPath))
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if InspectBundle(data) != Unpatched {
		// База пропатчена: оригинал — в резервной копии рядом.
		original += ".bak"
		if data, err = os.ReadFile(original); err != nil || InspectBundle(data) != Unpatched {
			t.Skip("непропатченной базы бандлов на этом компьютере нет")
		}
	}

	ctx := ctxFor(t, dir, dml, dmf, flux)
	ctx.Original = func(string) (string, error) { return original, nil }
	before, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(bundleDBPath)))

	files, notices, err := New().Generate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var patched string
	for _, f := range files {
		if f.Path == bundleDBPath {
			patched = f.Src
		}
	}
	if patched == "" {
		t.Fatalf("патч не собран: %s", titles(notices))
	}
	result, _ := os.ReadFile(patched)
	if InspectBundle(result) != PatchedDtkit || len(result) != len(data)+100 {
		t.Errorf("результат: состояние %v, размер %d (оригинал %d)", InspectBundle(result), len(result), len(data))
	}
	if after, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(bundleDBPath))); !bytes.Equal(before, after) {
		t.Fatal("тест изменил базу бандлов в папке игры")
	}
	// Если база в игре пропатчена dtkit-patch из того же оригинала, результат
	// должен совпасть с ней байт в байт.
	if strings.HasSuffix(original, ".bak") && InspectBundle(before) == PatchedDtkit && !bytes.Equal(result, before) {
		t.Error("наш патч отличается от того, что сделал dtkit-patch в игре")
	}
}

func TestDescribe(t *testing.T) {
	l := Describe([]string{"mods/afk/afk.mod", `mods\afk\scripts\afk.lua`, "README.md", "binaries/mod_loader", "mods/base/base.mod"})
	if l.Paths["README.md"] != "README.md" || l.Paths[`mods\afk\scripts\afk.lua`] != "mods/afk/scripts/afk.lua" {
		t.Errorf("пути: %v", l.Paths)
	}
	if strings.Join(l.Folders, ",") != "afk,base" || l.Role != game.RoleLoader {
		t.Errorf("папки %v, роль %q", l.Folders, l.Role)
	}
}

func TestOriginals(t *testing.T) {
	dir := newGame(t, fakeBundle(true))
	if got := New().Originals(dir); got != nil {
		t.Errorf("без .bak: %v", got)
	}
	bak := filepath.Join(dir, filepath.FromSlash(bundleDBPath)+".bak")
	os.WriteFile(bak, fakeBundle(false), 0o644)
	if got := New().Originals(dir); got[bundleDBPath] != bak {
		t.Errorf("с .bak: %v", got)
	}
	os.WriteFile(bak, fakeBundle(true), 0o644)
	if got := New().Originals(dir); got != nil {
		t.Errorf("пропатченный .bak принят за оригинал: %v", got)
	}
}

func TestLaunchCommand(t *testing.T) {
	dir := newGame(t, fakeBundle(false))
	cmd, err := launchCommand(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Запускается сама игра, а не лаунчер, из папки binaries.
	if cmd.Path != filepath.Join(dir, "binaries", "Darktide.exe") || cmd.Dir != filepath.Join(dir, "binaries") {
		t.Errorf("запуск %s из %s", cmd.Path, cmd.Dir)
	}
	if got := strings.Join(cmd.Args[1:], " "); got != "-eac-untrusted --bundle-dir ../bundle --ini settings --lua-heap-mb-size 2048" {
		t.Errorf("параметры: %s", got)
	}
	env := strings.Join(cmd.Env, ";")
	if !strings.Contains(env, "SteamAppId=1361210") || !strings.Contains(env, "SteamGameId=1361210") {
		t.Error("игре не сообщён её номер в Steam")
	}

	if _, err := launchCommand(t.TempDir()); err == nil {
		t.Error("запуск из папки без игры собран без ошибки")
	}
	if err := New().Launch(game.Install{Dir: dir, Store: "Xbox"}); err == nil {
		t.Error("запуск версии не из Steam прошёл")
	}
}
