package manager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/game"
)

// Диагностика после игры: игра сама пишет в журнал, какой мод выдал ошибку
// и из-за чего она упала. Modvault читает журнал последнего запуска и
// показывает это рядом с модами — искать виновника перебором не нужно.

// maxRunIssues — сколько модов с ошибками называть в замечаниях по
// отдельности; остальные сводятся в одну строку.
const maxRunIssues = 5

// runTrouble — ошибки мода набора в последнем запуске игры.
type runTrouble struct {
	count int
	first string
}

// runDiagnosis сопоставляет журнал последнего запуска игры с модами набора.
// Возвращает ошибки по модам (по идентификатору) и замечания для окна.
func (a *Manager) runDiagnosis(p profile.Profile) (map[string]runTrouble, []Issue) {
	rep, ok := a.game.LastRun()
	if !ok {
		return nil, nil
	}
	when := rep.Time.Local().Format("02.01.2006 15:04")
	var issues []Issue
	if rep.Crashed {
		issues = append(issues, Issue{
			Title:  "Прошлый запуск игры закончился сбоем (" + when + ")",
			Detail: crashReason(rep),
			Level:  LevelWarn,
		})
	}
	if len(rep.Mods) == 0 {
		return nil, issues
	}

	// Игра называет мод по папке, из которой его грузит.
	owner := map[string]profile.Entry{}
	names := map[string]string{}
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			continue
		}
		names[e.ModID] = v.Name
		// Версию сменили после того запуска — журнал уже не о ней.
		if v.Added.After(rep.Time) {
			continue
		}
		if l, err := a.layout(v); err == nil {
			for _, f := range l.Folders {
				owner[strings.ToLower(f)] = e
			}
		}
	}

	// Журнал может называть один мод по-разному (в разном регистре):
	// его ошибки складываются.
	troubles := map[string]runTrouble{}
	var order []string
	for _, m := range rep.Mods {
		e, ok := owner[strings.ToLower(m.Mod)]
		if !ok || !e.Enabled {
			continue // мод уже выключен, заменён или это не мод хранилища
		}
		t, seen := troubles[e.ModID]
		if !seen {
			order = append(order, e.ModID)
			t.first = m.First
		}
		t.count += m.Count
		troubles[e.ModID] = t
	}
	sort.SliceStable(order, func(i, j int) bool { return troubles[order[i]].count > troubles[order[j]].count })
	for i, id := range order {
		if i == maxRunIssues {
			rest := len(order) - maxRunIssues
			issues = append(issues, Issue{
				Title:  fmt.Sprintf("Ещё %d %s с ошибками в прошлом запуске игры", rest, plural(rest, "мод", "мода", "модов")),
				Detail: "Они отмечены значком ⚠ в списке; текст ошибки — в подсказке к значку",
				Level:  LevelWarn,
			})
			break
		}
		t := troubles[id]
		issues = append(issues, Issue{
			Title: fmt.Sprintf("«%s» выдал %d %s в прошлом запуске игры", names[id], t.count,
				plural(t.count, "ошибку", "ошибки", "ошибок")),
			Detail: "Запуск " + when + ". Первая: " + t.first,
			Level:  LevelWarn,
			Action: "Выключить «" + names[id] + "»", Command: "DisableMod", Arg: id,
		})
	}
	return troubles, issues
}

// crashReason объясняет причину сбоя по журналу игры.
func crashReason(rep game.RunReport) string {
	text := rep.CrashText
	if text == "" {
		text = "причина в журнале не названа"
	}
	switch strings.ToLower(rep.CrashKind) {
	case "memory":
		return "Игре не хватило памяти — это не ошибка мода. По журналу игры: " + text
	case "":
		return "По журналу игры: " + text
	}
	return fmt.Sprintf("Вид сбоя по журналу игры: %s. %s", rep.CrashKind, text)
}
