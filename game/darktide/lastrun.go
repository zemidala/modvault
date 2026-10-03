package darktide

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zemidala/modvault/game"
)

// Журналы игры: по файлу на каждый запуск. В них игра сама пишет, какой
// мод выдал ошибку и из-за чего она упала.
//
//	12:00:01.000 [Lua] [MOD][reliquary_hud][ERROR] Error processing '…': …
//	12:00:02.000 <<Crash>>Page allocator … failed allocating …
//	<<Crash type>>memory<</Crash type>>
var (
	modErrorLine = regexp.MustCompile(`\[MOD\]\[([^\]]+)\]\[ERROR\]\s*(.*)`)
	crashLine    = regexp.MustCompile(`<<Crash>>(.*)`)
	crashType    = regexp.MustCompile(`<<Crash type>>(.*?)<</Crash type>>`)
)

// maxMessage — сколько знаков текста ошибки хранить: в журнале бывают
// сообщения на несколько экранов.
const maxMessage = 300

// DefaultLogDir возвращает папку журналов игры у текущего пользователя.
func DefaultLogDir() string {
	base, err := os.UserConfigDir() // %APPDATA%
	if err != nil {
		return ""
	}
	return filepath.Join(base, "Fatshark", "Darktide", "console_logs")
}

// lastRunCache — разобранный журнал: разбирать его заново при каждом
// обновлении окна незачем, пока файл тот же.
type lastRunCache struct {
	path    string
	size    int64
	modTime int64
	report  game.RunReport
}

// LastRun разбирает журнал последнего запуска игры. ok ложно, если
// журналов нет или папка журналов не задана.
func (d *Darktide) LastRun() (game.RunReport, bool) {
	if d.LogDir == "" {
		return game.RunReport{}, false
	}
	entries, err := os.ReadDir(d.LogDir)
	if err != nil {
		return game.RunReport{}, false
	}
	var newest os.FileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == nil || info.ModTime().After(newest.ModTime()) {
			newest = info
		}
	}
	if newest == nil {
		return game.RunReport{}, false
	}
	path := filepath.Join(d.LogDir, newest.Name())

	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.lastRun; c != nil && c.path == path && c.size == newest.Size() && c.modTime == newest.ModTime().UnixNano() {
		return c.report, true
	}
	report, err := parseLog(path)
	if err != nil {
		return game.RunReport{}, false
	}
	report.Time, report.Log = newest.ModTime(), path
	d.lastRun = &lastRunCache{path: path, size: newest.Size(), modTime: newest.ModTime().UnixNano(), report: report}
	return report, true
}

// parseLog собирает из журнала ошибки модов и причину сбоя игры.
func parseLog(path string) (game.RunReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return game.RunReport{}, err
	}
	defer f.Close()

	var report game.RunReport
	byMod := map[string]*game.ModErrors{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20) // строки со стеком вызовов бывают длинными
	for sc.Scan() {
		line := sc.Text()
		if m := modErrorLine.FindStringSubmatch(line); m != nil {
			e, ok := byMod[m[1]]
			if !ok {
				e = &game.ModErrors{Mod: m[1], First: clip(m[2])}
				byMod[m[1]] = e
			}
			e.Count++
			continue
		}
		if m := crashLine.FindStringSubmatch(line); m != nil && !report.Crashed {
			report.Crashed, report.CrashText = true, clip(strings.TrimSuffix(m[1], "<</Crash>>"))
			continue
		}
		if m := crashType.FindStringSubmatch(line); m != nil {
			report.CrashKind = strings.TrimSpace(m[1])
		}
	}
	// Слишком длинная строка обрывает чтение; что успели разобрать — годится.
	for _, e := range byMod {
		report.Mods = append(report.Mods, *e)
	}
	sort.Slice(report.Mods, func(i, j int) bool {
		if report.Mods[i].Count != report.Mods[j].Count {
			return report.Mods[i].Count > report.Mods[j].Count
		}
		return report.Mods[i].Mod < report.Mods[j].Mod
	})
	return report, nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxMessage {
		return string(r[:maxMessage]) + "…"
	}
	return s
}
