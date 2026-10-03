package darktide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Строки взяты из настоящих журналов игры.
const sampleLog = `[Log version] 1
10:36:06.568 [Application] STARTUP: setup_crash_report
11:06:40.100 [Lua] [MOD][DMF][INFO] Mods loaded
11:06:40.182 [Lua] [MOD][reliquary_hud][ERROR] Error processing './../mods/reliquary_hud/scripts/mods/reliquary_hud/hud/rh_definitions.lua': [string "./../mods/reliquary_hud/scripts/mods/reliquar..."]:57: attempt to index global 'Orb' (a nil value)
11:06:41.000 [Lua] [MOD][stamina_dodge_bars][ERROR] (localize): localization file was not loaded for this mod
11:06:42.000 [Lua] [MOD][stamina_dodge_bars][ERROR] (localize): localization file was not loaded for this mod
11:06:43.000 [Lua] [MOD][Many Mission Terminal][INFO] ready
11:06:44.000 [Lua] [MOD][stamina_dodge_bars][ERROR] (localize): localization file was not loaded for this mod
17:26:30.752 <<Crash>>Page allocator 'render_page_allocator' failed allocating 65536 bytes, total 394592256
<</Crash>>
<<crashify-property>>Mod:afk = true<</crashify-property>>
<<Crash version>>1<</Crash version>>
<<Crash type>>memory<</Crash type>>
[Log end]
`

func writeLog(t *testing.T, dir, name, content string, at time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLastRun(t *testing.T) {
	// Папка журналов не задана или пуста — отчёта нет.
	if _, ok := New().LastRun(); ok {
		t.Error("отчёт без папки журналов")
	}
	dir := t.TempDir()
	d := &Darktide{LogDir: dir}
	if _, ok := d.LastRun(); ok {
		t.Error("отчёт из пустой папки")
	}

	now := time.Now().Truncate(time.Second)
	writeLog(t, dir, "console-old.log", "10:00:00.000 [Lua] [MOD][old_mod][ERROR] давняя ошибка\n", now.Add(-2*time.Hour))
	last := writeLog(t, dir, "console-new.log", sampleLog, now.Add(-time.Hour))
	writeLog(t, dir, "notes.txt", "[MOD][x][ERROR] не журнал\n", now)

	rep, ok := d.LastRun()
	if !ok {
		t.Fatal("отчёта нет")
	}
	if rep.Log != last || !rep.Time.Equal(now.Add(-time.Hour)) {
		t.Errorf("взят журнал %s от %v", rep.Log, rep.Time)
	}
	if !rep.Crashed || rep.CrashKind != "memory" || !strings.HasPrefix(rep.CrashText, "Page allocator 'render_page_allocator' failed") {
		t.Errorf("сбой: %v %q %q", rep.Crashed, rep.CrashKind, rep.CrashText)
	}
	if len(rep.Mods) != 2 {
		t.Fatalf("моды с ошибками: %+v", rep.Mods)
	}
	if m := rep.Mods[0]; m.Mod != "stamina_dodge_bars" || m.Count != 3 || m.First != "(localize): localization file was not loaded for this mod" {
		t.Errorf("самый шумный мод: %+v", m)
	}
	if m := rep.Mods[1]; m.Mod != "reliquary_hud" || m.Count != 1 || !strings.Contains(m.First, "attempt to index global 'Orb'") {
		t.Errorf("второй мод: %+v", m)
	}

	// Новый запуск игры — новый журнал: отчёт о нём, без сбоя.
	writeLog(t, dir, "console-newest.log", "12:00:00.000 [Lua] [MOD][DMF][INFO] ok\n", now.Add(time.Minute))
	rep, ok = d.LastRun()
	if !ok || rep.Crashed || len(rep.Mods) != 0 {
		t.Errorf("чистый запуск: %+v, %v", rep, ok)
	}

	// Очень длинное сообщение обрезается.
	writeLog(t, dir, "console-long.log", "12:00:00.000 [Lua] [MOD][big][ERROR] "+strings.Repeat("я", 5000)+"\n", now.Add(2*time.Minute))
	if rep, _ = d.LastRun(); len(rep.Mods) != 1 || len([]rune(rep.Mods[0].First)) != maxMessage+1 {
		t.Errorf("длинное сообщение: %d знаков", len([]rune(rep.Mods[0].First)))
	}
}
