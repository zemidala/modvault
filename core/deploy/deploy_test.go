package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/manifest"
)

// mod создаёт файлы версии мода в папке хранилища root (если их ещё нет)
// и возвращает Source. Путь файла в игре совпадает с путём в моде.
func mod(t testing.TB, root, id, version string, files map[string]string) Source {
	t.Helper()
	s := Source{ModID: id, VersionID: version}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		src := filepath.Join(root, id, version, filepath.FromSlash(p))
		if _, err := os.Stat(src); err != nil {
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := fsx.WriteFile(src, []byte(files[p])); err != nil {
				t.Fatal(err)
			}
		}
		hash, size, err := fsx.HashFile(src)
		if err != nil {
			t.Fatal(err)
		}
		s.Files = append(s.Files, File{Path: p, Src: src, Hash: hash, Size: size})
	}
	return s
}

// snapshot описывает папку целиком: файлы с содержимым и папки.
func snapshot(t testing.TB, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			lines = append(lines, rel+"/")
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines = append(lines, rel+" = "+string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func writeTree(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type env struct {
	game, state, store string
	d                  *Deployer
}

func newEnv(t testing.TB, foreign map[string]string) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{
		game:  filepath.Join(base, "Игра"),
		state: filepath.Join(base, "Хранилище", "deploy"),
		store: filepath.Join(base, "Хранилище", "mods"),
	}
	if err := os.MkdirAll(e.game, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, e.game, foreign)
	d, rec, err := Open(e.game, e.state)
	if err != nil || rec != NothingToRecover {
		t.Fatalf("Open: %v, %v", rec, err)
	}
	e.d = d
	return e
}

func (e *env) deploy(t testing.TB, sources ...Source) (*Plan, Result) {
	t.Helper()
	p, err := e.d.Plan(sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	after, err := e.d.Plan(sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Empty() {
		t.Fatalf("после развёртывания план не пуст: %+v", after.Changes)
	}
	return p, res
}

var foreignFiles = map[string]string{
	"Darktide.exe":       "игра",
	"mods/base.lua":      "файл игры",
	"bundle/данные.data": "бандл",
}

func TestDeployAndUndeploy(t *testing.T) {
	e := newEnv(t, foreignFiles)
	clean := snapshot(t, e.game)

	a := mod(t, e.store, "a", "1", map[string]string{
		"mods/a/a.mod":         "мод A",
		"mods/a/scripts/a.lua": "код A",
		"mods/base.lua":        "A заменяет файл игры",
	})
	b := mod(t, e.store, "b", "1", map[string]string{"mods/Б/б.mod": "мод Б"})

	p, _ := e.deploy(t, a, b)
	if len(p.Changes) != 4 || len(p.Conflicts) != 0 || len(p.Drift) != 0 {
		t.Errorf("план: %d изменений, %d конфликтов, %d расхождений", len(p.Changes), len(p.Conflicts), len(p.Drift))
	}
	got := snapshot(t, e.game)
	for _, want := range []string{"mods/a/scripts/a.lua = код A", "mods/base.lua = A заменяет файл игры", "mods/Б/б.mod = мод Б", "Darktide.exe = игра"} {
		if !strings.Contains(got, want) {
			t.Errorf("в игре нет %q:\n%s", want, got)
		}
	}

	m, _ := e.d.Manifest()
	if entry, ok := m.Get("MODS/BASE.LUA"); !ok || entry.ModID != "a" || !entry.Backup {
		t.Errorf("учёт файла игры: %+v, %v", entry, ok)
	}
	if entry, _ := m.Get("mods/a/a.mod"); entry.Method == "" || entry.Deployed.IsZero() {
		t.Errorf("учёт файла мода: %+v", entry)
	}

	// Выключить всё: игра побайтно как до модов.
	p, _ = e.deploy(t)
	if len(p.Changes) != 4 {
		t.Errorf("снятие: %d изменений", len(p.Changes))
	}
	if after := snapshot(t, e.game); after != clean {
		t.Errorf("после снятия игра отличается от исходной:\n%s\n---\n%s", after, clean)
	}
	if m, _ := e.d.Manifest(); m.Len() != 0 || len(m.Dirs()) != 0 {
		t.Errorf("учёт не пуст: %d файлов, папки %v", m.Len(), m.Dirs())
	}
	if left := snapshot(t, filepath.Join(e.state, backupsDir)); strings.Contains(left, "=") {
		t.Errorf("в резервных копиях остались файлы:\n%s", left)
	}
}

func TestLinkAndCopy(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("копия=%v", force), func(t *testing.T) {
			e := newEnv(t, nil)
			e.d.ForceCopy = force
			a := mod(t, e.store, "a", "1", map[string]string{"x.lua": "x"})
			e.deploy(t, a)

			si, _ := os.Stat(a.Files[0].Src)
			gi, _ := os.Stat(filepath.Join(e.game, "x.lua"))
			linked := os.SameFile(si, gi)
			m, _ := e.d.Manifest()
			entry, _ := m.Get("x.lua")
			if force && (linked || entry.Method != "copy") {
				t.Errorf("при запрете ссылок: ссылка=%v, способ %q", linked, entry.Method)
			}
			if !force && linked != (entry.Method == "link") {
				t.Errorf("учёт говорит %q, а ссылка=%v", entry.Method, linked)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	e := newEnv(t, nil)
	a := mod(t, e.store, "a", "1", map[string]string{"shared.lua": "от A", "a.lua": "a"})
	b := mod(t, e.store, "b", "1", map[string]string{"SHARED.lua": "от B"})
	c := mod(t, e.store, "c", "1", map[string]string{"shared.lua": "от C"})

	p, _ := e.deploy(t, a, b, c)
	if len(p.Conflicts) != 1 || p.Conflicts[0].Winner != "c" || strings.Join(p.Conflicts[0].Mods, ",") != "a,b,c" {
		t.Errorf("конфликты: %+v", p.Conflicts)
	}
	if data, _ := os.ReadFile(filepath.Join(e.game, "shared.lua")); string(data) != "от C" {
		t.Errorf("победил не последний: %q", data)
	}

	// Закреплённый победитель важнее порядка.
	winners := map[string]string{"Shared.LUA": "a"}
	plan, err := e.d.Plan([]Source{a, b, c}, winners)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Conflicts[0].Winner != "a" {
		t.Errorf("закреплённый победитель: %+v", plan.Conflicts[0])
	}
	if _, err := e.d.Apply(plan); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(e.game, "shared.lua")); string(data) != "от A" {
		t.Errorf("после закрепления: %q", data)
	}

	// Закрепление за модом, которого нет среди претендентов, не действует.
	plan, _ = e.d.Plan([]Source{a, b, c}, map[string]string{"shared.lua": "нет_такого"})
	if plan.Conflicts[0].Winner != "c" {
		t.Errorf("закрепление за посторонним: %+v", plan.Conflicts[0])
	}
}

func TestReplaceVersion(t *testing.T) {
	e := newEnv(t, foreignFiles)
	clean := snapshot(t, e.game)
	v1 := mod(t, e.store, "a", "1", map[string]string{"mods/a/a.lua": "версия 1", "mods/a/old.lua": "только в 1", "mods/base.lua": "A1"})
	v2 := mod(t, e.store, "a", "2", map[string]string{"mods/a/a.lua": "версия 2", "mods/a/new/n.lua": "только в 2", "mods/base.lua": "A2"})

	e.deploy(t, v1)
	p, _ := e.deploy(t, v2)
	kinds := map[ChangeKind]int{}
	for _, c := range p.Changes {
		kinds[c.Kind]++
	}
	if kinds[Replace] != 2 || kinds[Add] != 1 || kinds[Remove] != 1 {
		t.Errorf("смена версии: %v", kinds)
	}
	got := snapshot(t, e.game)
	if !strings.Contains(got, "mods/a/a.lua = версия 2") || strings.Contains(got, "old.lua") || !strings.Contains(got, "mods/base.lua = A2") {
		t.Errorf("после смены версии:\n%s", got)
	}

	// Файл игры, заменённый ещё первой версией, возвращается и после второй.
	e.deploy(t)
	if after := snapshot(t, e.game); after != clean {
		t.Errorf("после снятия:\n%s\n---\n%s", after, clean)
	}
}

func TestDrift(t *testing.T) {
	e := newEnv(t, foreignFiles)
	clean := snapshot(t, e.game)
	a := mod(t, e.store, "a", "1", map[string]string{"mods/a/edited.lua": "оригинал", "mods/a/lost.lua": "пропадёт", "mods/a/ok.lua": "ok"})
	e.deploy(t, a)

	edited := filepath.Join(e.game, "mods", "a", "edited.lua")
	os.Remove(edited) // ссылку правим заменой файла, чтобы не испортить хранилище
	os.WriteFile(edited, []byte("правка пользователя"), 0o644)
	os.Remove(filepath.Join(e.game, "mods", "a", "lost.lua"))

	p, err := e.d.Plan([]Source{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drift := map[string]bool{}
	for _, d := range p.Drift {
		drift[d.Path] = d.Missing
	}
	if len(drift) != 2 || drift["mods/a/edited.lua"] || !drift["mods/a/lost.lua"] {
		t.Fatalf("расхождения: %+v", p.Drift)
	}

	// Развёртывание кладёт файлы заново, а правку пользователя уносит в сторону.
	res, err := e.d.Apply(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Displaced == "" {
		t.Fatal("правка пользователя никуда не перенесена")
	}
	if data, err := os.ReadFile(filepath.Join(res.Displaced, "mods", "a", "edited.lua")); err != nil || string(data) != "правка пользователя" {
		t.Errorf("перенесённая правка: %q, %v", data, err)
	}
	if got := snapshot(t, e.game); !strings.Contains(got, "edited.lua = оригинал") || !strings.Contains(got, "lost.lua = пропадёт") {
		t.Errorf("после развёртывания:\n%s", got)
	}

	e.deploy(t)
	if after := snapshot(t, e.game); after != clean {
		t.Errorf("после снятия:\n%s\n---\n%s", after, clean)
	}
}

// replaceFile заменяет файл новым, как это делает Steam: ссылка на файл
// в хранилище при этом не портится.
func replaceFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Обновление игры заменило файл, который мод подменял. Старая резервная
// копия не возвращается никогда: оригиналом становится новый файл.
func TestGameUpdateNeverRestoresStaleBackup(t *testing.T) {
	for _, keep := range []bool{true, false} {
		t.Run(fmt.Sprintf("мод остаётся=%v", keep), func(t *testing.T) {
			e := newEnv(t, map[string]string{"bundle/db.data": "версия 1"})
			patched1 := mod(t, e.store, "patch", "1", map[string]string{"bundle/db.data": "патч к версии 1"})
			e.deploy(t, patched1)

			gameFile := filepath.Join(e.game, "bundle", "db.data")
			replaceFile(t, gameFile, "версия 2")

			if orig, err := e.d.Original("bundle/db.data"); err != nil || orig != gameFile {
				t.Fatalf("оригинал после обновления: %q, %v", orig, err)
			}
			p, err := e.d.Plan([]Source{patched1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Drift) != 1 || !p.Drift[0].Updated {
				t.Fatalf("обновление не распознано: %+v", p.Drift)
			}

			var sources []Source
			if keep {
				sources = []Source{mod(t, e.store, "patch", "2", map[string]string{"bundle/db.data": "патч к версии 2"})}
			}
			e.deploy(t, sources...)
			want := "версия 2"
			if keep {
				want = "патч к версии 2"
			}
			if data, _ := os.ReadFile(gameFile); string(data) != want {
				t.Fatalf("после развёртывания: %q, want %q", data, want)
			}

			// Снять всё: в игре остаётся версия 2, а не устаревшая версия 1.
			e.deploy(t)
			if data, _ := os.ReadFile(gameFile); string(data) != "версия 2" {
				t.Errorf("после снятия: %q — вернулась устаревшая резервная копия", data)
			}
			if left := snapshot(t, filepath.Join(e.state, backupsDir)); strings.Contains(left, "=") {
				t.Errorf("резервные копии остались:\n%s", left)
			}
		})
	}
}

func TestOriginal(t *testing.T) {
	e := newEnv(t, map[string]string{"bundle/db.data": "оригинал"})
	a := mod(t, e.store, "a", "1", map[string]string{"bundle/db.data": "мод", "mods/a/a.mod": "a"})
	if orig, _ := e.d.Original("bundle/db.data"); orig != filepath.Join(e.game, "bundle", "db.data") {
		t.Errorf("до развёртывания: %q", orig)
	}
	e.deploy(t, a)
	orig, err := e.d.Original("BUNDLE/db.data")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(orig); string(data) != "оригинал" {
		t.Errorf("оригинал заменённого файла: %q", data)
	}
	if _, err := e.d.Original("mods/a/a.mod"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("у файла, принесённого модом, нашёлся оригинал: %v", err)
	}
}

func TestPlanRejects(t *testing.T) {
	e := newEnv(t, map[string]string{"dir/file/inner.txt": "x"})
	bad := Source{ModID: "bad", VersionID: "1", Files: []File{{Path: "../outside.lua", Src: "x"}}}
	if _, err := e.d.Plan([]Source{bad}, nil); !errors.Is(err, fsx.ErrUnsafePath) {
		t.Errorf("путь наружу: %v, want ErrUnsafePath", err)
	}
	dirMod := mod(t, e.store, "d", "1", map[string]string{"dir/file": "файл на месте папки"})
	if _, err := e.d.Plan([]Source{dirMod}, nil); err == nil {
		t.Error("файл на месте папки принят")
	}
}

func TestOpenErrors(t *testing.T) {
	base := t.TempDir()
	if _, _, err := Open(filepath.Join(base, "нет"), filepath.Join(base, "state")); err == nil {
		t.Error("Open несуществующей папки игры прошёл")
	}
	file := filepath.Join(base, "файл")
	os.WriteFile(file, nil, 0o644)
	if _, _, err := Open(file, filepath.Join(base, "state")); err == nil {
		t.Error("Open файла вместо папки прошёл")
	}
}

// Обрыв на каждом шаге. Сценарий: в игре развёрнут набор P1, развёртывается
// P2 — со сменой версии, снятием мода, новым модом, конфликтом и заменой
// файла игры. Вспомогательный процесс обрывается до шага, после шага
// (до отметки в журнале) и после записи учёта. После перезапуска игра
// обязана побайтно совпадать либо с P1, либо с P2.
const (
	crashGame  = "MODVAULT_DEPLOY_CRASH_GAME"
	crashState = "MODVAULT_DEPLOY_CRASH_STATE"
	crashStore = "MODVAULT_DEPLOY_CRASH_STORE"
	crashPoint = "MODVAULT_DEPLOY_CRASH_POINT"
	crashCode  = 3
)

func sets(t testing.TB, store string) (p1, p2 []Source) {
	a1 := mod(t, store, "a", "1", map[string]string{"mods/a/a.mod": "A1", "mods/a/only1.lua": "только 1", "mods/base.lua": "A1 база"})
	a2 := mod(t, store, "a", "2", map[string]string{"mods/a/a.mod": "A2", "mods/a/sub/only2.lua": "только 2", "mods/base.lua": "A2 база"})
	b := mod(t, store, "b", "1", map[string]string{"mods/b/b.mod": "B", "bundle/данные.data": "B бандл"})
	c := mod(t, store, "c", "1", map[string]string{"mods/c/deep/c.mod": "C", "mods/base.lua": "C база", "Darktide.exe": "C поверх игры"})
	return []Source{a1, b}, []Source{a2, c}
}

// setupP1 разворачивает P1, а затем «обновляет игру»: Steam заменяет файл
// игры, который подменял мод B. P2 мода B уже не содержит.
func setupP1(t testing.TB, e *env, p1 []Source) {
	t.Helper()
	e.deploy(t, p1...)
	replaceFile(t, filepath.Join(e.game, "bundle", "данные.data"), "бандл после обновления игры")
}

func TestCrashHelper(t *testing.T) {
	point := os.Getenv(crashPoint)
	if point == "" {
		t.Skip("вспомогательный процесс для TestKillAtEveryStep")
	}
	d, _, err := Open(os.Getenv(crashGame), os.Getenv(crashState))
	if err != nil {
		t.Fatal(err)
	}
	_, p2 := sets(t, os.Getenv(crashStore))
	plan, err := d.Plan(p2, nil)
	if err != nil {
		t.Fatal(err)
	}
	testHook = func(at string, i int) {
		if fmt.Sprintf("%s:%d", at, i) == point {
			os.Exit(crashCode)
		}
	}
	d.Apply(plan)
	t.Fatalf("точка %s не встретилась", point)
}

func TestKillAtEveryStep(t *testing.T) {
	if testing.Short() {
		t.Skip("запускает много процессов")
	}

	// Эталон: P1, затем P2 без обрывов.
	ref := newEnv(t, foreignFiles)
	p1, p2 := sets(t, ref.store)
	setupP1(t, ref, p1)
	want1 := snapshot(t, ref.game)
	before, err := ref.d.Plan(p1, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ref.d.Plan(p2, nil)
	if err != nil {
		t.Fatal(err)
	}
	steps := len(plan.steps)
	ref.deploy(t, p2...)
	want2 := snapshot(t, ref.game)
	if steps < 10 {
		t.Fatalf("сценарий слишком простой: %d шагов", steps)
	}

	var points []string
	for i := 0; i < steps; i++ {
		points = append(points, fmt.Sprintf("before:%d", i), fmt.Sprintf("after:%d", i))
	}
	points = append(points, fmt.Sprintf("commit:%d", steps))

	// Обрыв программы отметок журнала не теряет; отключение питания может
	// потерять последние — вплоть до всех. Восстановление обязано вернуть
	// игру на место в любом случае.
	marks := map[string]func(lines []string) []string{
		"отметки целы":     func(lines []string) []string { return lines },
		"отметки пропали":  func(lines []string) []string { return nil },
		"пропала половина": func(lines []string) []string { return lines[:len(lines)/2] },
		"пропала последняя": func(lines []string) []string {
			if len(lines) == 0 {
				return lines
			}
			return lines[:len(lines)-1]
		},
	}
	for _, point := range points {
		for lost, keep := range marks {
			killAt(t, point, lost, keep, want1, want2, before)
		}
	}
}

// killAt обрывает развёртывание P2 в точке point, оставляет в журнале
// отметки, которые выбрал keep, и проверяет восстановление.
func killAt(t *testing.T, point, lost string, keep func([]string) []string, want1, want2 string, before *Plan) {
	t.Run(point+"/"+lost, func(t *testing.T) {
		{
			e := newEnv(t, foreignFiles)
			p1, p2 := sets(t, e.store)
			setupP1(t, e, p1)

			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), crashGame+"="+e.game, crashState+"="+e.state, crashStore+"="+e.store, crashPoint+"="+point)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != crashCode {
				t.Fatalf("вспомогательный процесс: %v\n%s", err, out)
			}
			logPath := filepath.Join(e.state, "journal.log")
			if data, err := os.ReadFile(logPath); err == nil {
				lines := keep(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"))
				text := ""
				if len(lines) > 0 {
					text = strings.Join(lines, "\n") + "\n"
				}
				if err := os.WriteFile(logPath, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			d, rec, err := Open(e.game, e.state)
			if err != nil {
				t.Fatalf("восстановление: %v", err)
			}
			e.d = d
			committed := strings.HasPrefix(point, "commit")
			switch {
			case committed && rec != Completed, !committed && rec != RolledBack:
				t.Errorf("восстановление = %v", rec)
			}
			got := snapshot(t, e.game)
			want, current := want1, p1
			if committed {
				want, current = want2, p2
			}
			if got != want {
				t.Fatalf("после восстановления игра не совпала ни с P1, ни с P2:\n%s\n--- ожидалось ---\n%s", got, want)
			}

			// Учёт согласован с диском: после отката план тот же, что до обрыва,
			// после завершения — пуст. Следующее развёртывание проходит.
			plan, err := d.Plan(current, nil)
			if err != nil {
				t.Fatal(err)
			}
			if committed && (!plan.Empty() || len(plan.Drift) != 0) {
				t.Fatalf("учёт расходится с диском: %+v", plan)
			}
			if !committed && (len(plan.steps) != len(before.steps) || len(plan.Drift) != len(before.Drift)) {
				t.Fatalf("после отката план другой: %d шагов и %d расхождений, было %d и %d",
					len(plan.steps), len(plan.Drift), len(before.steps), len(before.Drift))
			}
			e.deploy(t, p2...)
			if got := snapshot(t, e.game); got != want2 {
				t.Fatalf("развёртывание после восстановления:\n%s", got)
			}
			if aside := snapshot(t, filepath.Join(e.state, stashDir)); strings.Contains(aside, "=") {
				t.Errorf("в stash остались файлы:\n%s", aside)
			}
		}
	})
}

// Файлы другого менеджера переходят под учёт без изменений в игре; после
// этого снятие возвращает сохранённые оригиналы, а Forget снимает учёт,
// оставляя игру как есть.
func TestAdoptAndForget(t *testing.T) {
	e := newEnv(t, map[string]string{"mods/base.lua": "мод другого менеджера", "keep.txt": "чужой"})
	original := filepath.Join(t.TempDir(), "base.lua.backup")
	os.WriteFile(original, []byte("файл игры"), 0o644)
	a := mod(t, e.store, "a", "1", map[string]string{"mods/base.lua": "мод другого менеджера"})
	before := snapshot(t, e.game)

	if err := e.d.Adopt("adopted-1", []AdoptEntry{{
		Entry:    manifestEntry(a.Files[0], "a", "1"),
		Original: original,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, e.game); got != before {
		t.Fatal("Adopt изменил игру")
	}
	if p, _ := e.d.Plan([]Source{a}, nil); !p.Empty() || len(p.Drift) != 0 {
		t.Fatalf("после Adopt план не пуст: %+v", p)
	}
	if err := e.d.Adopt("adopted-2", nil); err == nil {
		t.Error("второй Adopt поверх учёта прошёл")
	}

	e.deploy(t)
	if data, _ := os.ReadFile(filepath.Join(e.game, "mods", "base.lua")); string(data) != "файл игры" {
		t.Errorf("после снятия: %q, want сохранённый оригинал", data)
	}

	e.deploy(t, a)
	if err := e.d.Forget(); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.d.Manifest(); m.Len() != 0 {
		t.Error("учёт остался после Forget")
	}
	if data, _ := os.ReadFile(filepath.Join(e.game, "mods", "base.lua")); string(data) != "мод другого менеджера" {
		t.Errorf("Forget изменил игру: %q", data)
	}
}

func manifestEntry(f File, modID, versionID string) manifest.Entry {
	return manifest.Entry{Path: f.Path, ModID: modID, VersionID: versionID, Hash: f.Hash, Size: f.Size, Method: manifest.MethodCopy}
}
