package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zemidala/modvault/game/darktide"
)

// hunt ведёт поиск до конца, отвечая за игру: проблема есть, если сбойный
// мод включён. Возвращает итог и число запусков игры.
func hunt(t *testing.T, a *Manager, culprit string) (BisectResult, int) {
	t.Helper()
	return huntWith(t, a, func(s State) bool { return findMod(t, s, culprit).Enabled })
}

// huntWith ведёт поиск до конца; problem отвечает за игру, есть ли
// проблема с модами, включёнными в этом шаге.
func huntWith(t *testing.T, a *Manager, problem func(State) bool) (BisectResult, int) {
	t.Helper()
	res, err := a.StartBisect()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for runs := 1; runs < 40; runs++ {
		if res.State.Profile != BisectSet || res.State.Bisect == nil {
			t.Fatalf("шаг %d: поиск не показан окну: набор %q, ход %+v", runs, res.State.Profile, res.State.Bisect)
		}
		if res.State.PlanTitle != "" {
			t.Fatalf("шаг %d: игра не приведена к шагу: %q", runs, res.State.PlanTitle)
		}
		if res.Done {
			t.Fatalf("шаг %d: поиск идёт, а итог отмечен законченным", runs)
		}
		// Один и тот же состав модов дважды не запускается.
		if key := enabledIDs(res.State); seen[key] {
			t.Fatalf("шаг %d: состав %s уже проверялся", runs, key)
		} else {
			seen[key] = true
		}
		if res, err = a.BisectAnswer(problem(res.State)); err != nil {
			t.Fatal(err)
		}
		if res.State.Profile != BisectSet {
			if !res.Done {
				t.Errorf("поиск закончен, а итог не отмечен: %q", res.Message)
			}
			return res, runs
		}
	}
	t.Fatal("поиск не закончился")
	return BisectResult{}, 0
}

func culpritIDs(res BisectResult) string {
	ids := make([]string, len(res.Culprits))
	for i, c := range res.Culprits {
		ids[i] = c.ID
	}
	return strings.Join(ids, ",")
}

func TestBisect(t *testing.T) {
	// Пять подозреваемых: needy → helper → deep (зависимости), plain, other.
	for _, culprit := range []string{"needy", "helper", "deep", "plain", "other"} {
		a, g := setsApp(t)
		before := snapshot(t, g)
		all := enabledIDs(state(t, a))

		res, runs := hunt(t, a, culprit)
		if res.Culprit != culprit || culpritIDs(res) != culprit || !strings.Contains(res.Message, "Найден сбойный мод") {
			t.Errorf("%s: найден %q за %d запусков: %s", culprit, res.Culprit, runs, res.Message)
		}
		// Запуск без модов, три шага деления, проверка найденного и, если
		// проблема до конца ни разу не повторилась, запуск с полным набором.
		if runs > 6 {
			t.Errorf("%s: запусков игры %d, а подозреваемых всего пять", culprit, runs)
		}
		// Свой набор цел, игра вернулась к нему, временный набор убран.
		if res.State.Profile != mainProfile || enabledIDs(res.State) != all || res.State.PlanTitle != "" || res.State.Bisect != nil {
			t.Errorf("%s: после поиска набор %q, включены %s, план %q", culprit, res.State.Profile, enabledIDs(res.State), res.State.PlanTitle)
		}
		if !sameTree(before, snapshot(t, g)) {
			t.Errorf("%s: игра после поиска не такая, как до него", culprit)
		}
		if sets, _ := a.Sets(); len(sets) != 1 {
			t.Errorf("%s: наборы после поиска: %+v", culprit, sets)
		}
		for _, m := range res.State.Mods {
			if strings.Contains(strings.Join(m.Sets, ","), BisectSet) {
				t.Errorf("%s: мод %s числится во временном наборе", culprit, m.ID)
			}
		}
	}
}

func TestBisectLarge(t *testing.T) {
	a, _, g := newGame(t)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	for i := 0; i < 40; i++ {
		a.addArchive(modZip(t, fmt.Sprintf("Mod%02d", i), `return {}`))
	}
	a.Deploy()
	res, runs := hunt(t, a, "mod27")
	if res.Culprit != "mod27" || runs > 9 {
		t.Errorf("найден %q за %d запусков игры: %s", res.Culprit, runs, res.Message)
	}

	// Сочетание трёх модов: проблема есть, только когда включены все три.
	trio := []string{"mod03", "mod19", "mod38"}
	res, runs = huntWith(t, a, func(s State) bool {
		for _, id := range trio {
			if !findMod(t, s, id).Enabled {
				return false
			}
		}
		return true
	})
	if len(res.Culprits) != 3 || res.Culprit != "" || !strings.Contains(res.Message, "сочетание модов") {
		t.Fatalf("сочетание трёх: %q за %d запусков: %s", culpritIDs(res), runs, res.Message)
	}
	for _, id := range trio {
		if !strings.Contains(culpritIDs(res), id) {
			t.Errorf("в сочетании нет %s: %s", id, culpritIDs(res))
		}
	}
	if runs > 25 {
		t.Errorf("сочетание трёх из сорока: запусков игры %d", runs)
	}
}

// Виновник назван, только если проблема повторилась с ним одним, а
// сочетание модов находится целиком.
func TestBisectCombination(t *testing.T) {
	a, g := setsApp(t)
	before := snapshot(t, g)
	// Проблему вызывает сочетание двух модов: по отдельности каждый исправен.
	res, _ := huntWith(t, a, func(s State) bool {
		return findMod(t, s, "plain").Enabled && findMod(t, s, "other").Enabled
	})
	if got := culpritIDs(res); (got != "plain,other" && got != "other,plain") || res.Culprit != "" {
		t.Errorf("сочетание модов: найдено %q: %s", got, res.Message)
	}
	if !strings.Contains(res.Message, "сочетание модов") || !strings.Contains(res.Message, "«Plain»") || !strings.Contains(res.Message, "«Other»") {
		t.Errorf("сообщение о сочетании: %s", res.Message)
	}
	if res.State.Profile != mainProfile || res.State.Bisect != nil || !sameTree(before, snapshot(t, g)) {
		t.Errorf("после поиска набор %q, игра не возвращена", res.State.Profile)
	}

	// Мод и тот, кого он требует: виновники оба, хотя включаются вместе.
	b, _ := setsApp(t)
	res, _ = huntWith(t, b, func(s State) bool {
		return findMod(t, s, "needy").Enabled && findMod(t, s, "plain").Enabled
	})
	if got := culpritIDs(res); got != "plain,needy" && got != "needy,plain" {
		t.Errorf("сочетание с зависимым модом: найдено %q: %s", got, res.Message)
	}
}

// Поиск никого не обвиняет, если дело не в модах или проблема непостоянна.
func TestBisectNoCulprit(t *testing.T) {
	// Проблема есть и без модов: первый же запуск это показывает.
	a, g := setsApp(t)
	before := snapshot(t, g)
	res, runs := huntWith(t, a, func(State) bool { return true })
	if runs != 1 || len(res.Culprits) != 0 || !strings.Contains(res.Message, "Дело не в модах") {
		t.Errorf("проблема без модов: запусков %d, найдено %q: %s", runs, culpritIDs(res), res.Message)
	}
	if res.State.Profile != mainProfile || !sameTree(before, snapshot(t, g)) {
		t.Error("после поиска без виновника игра не возвращена")
	}

	// Проблема не повторилась ни разу, даже с полным набором.
	b, _ := setsApp(t)
	res, runs = huntWith(t, b, func(State) bool { return false })
	if len(res.Culprits) != 0 || !strings.Contains(res.Message, "С полным набором модов проблема не повторилась") {
		t.Errorf("проблема не повторяется: найдено %q: %s", culpritIDs(res), res.Message)
	}
	if runs > 5 {
		t.Errorf("проблема не повторяется: запусков %d", runs)
	}
}

func TestBisectCancelAndErrors(t *testing.T) {
	a, g := setsApp(t)
	before := snapshot(t, g)
	if _, err := a.BisectAnswer(true); err == nil {
		t.Error("ответ без поиска принят")
	}
	if _, err := a.CancelBisect(); err == nil {
		t.Error("прерывание без поиска прошло")
	}
	res, err := a.StartBisect()
	if err != nil {
		t.Fatal(err)
	}
	// Первый шаг — без модов набора: в игре только загрузчик и фреймворк.
	st := res.State.Bisect
	if st.Step != 1 || st.Suspects != 5 || st.Steps != 5 || st.Origin != mainProfile || st.Kind != "base" || len(st.Testing) != 0 {
		t.Errorf("ход поиска: %+v", st)
	}
	if enabledIDs(res.State) != "dml,dmf" || !strings.Contains(res.Message, "моды набора выключены") {
		t.Errorf("первый шаг: включены %s, %q", enabledIDs(res.State), res.Message)
	}
	if _, err := a.StartBisect(); err == nil {
		t.Error("второй поиск поверх первого начат")
	}
	// Пока идёт поиск, набор руками не правится, а исходный не удаляется.
	if _, err := a.SetEnabled("plain", false); err == nil {
		t.Error("мод выключен руками посреди поиска")
	}
	if _, err := a.SetEnabledMany([]string{"plain"}, true); err == nil {
		t.Error("моды включены руками посреди поиска")
	}
	if _, err := a.DeleteSet(mainProfile); err == nil {
		t.Error("исходный набор удалён посреди поиска")
	}
	if _, err := a.RenameSet(mainProfile, "Другой"); err == nil {
		t.Error("исходный набор переименован посреди поиска")
	}
	// Поиск переживает перезапуск программы.
	b := NewWith(a.home, a.game)
	if st := state(t, b).Bisect; st == nil || st.Step != 1 {
		t.Fatalf("после перезапуска: %+v", st)
	}
	// Второй шаг — деление: включена часть подозреваемых.
	res, err = b.BisectAnswer(false)
	if err != nil {
		t.Fatal(err)
	}
	if st := res.State.Bisect; st.Step != 2 || st.Kind != "search" || len(st.Testing) == 0 || len(st.Testing) >= 5 {
		t.Errorf("второй шаг: %+v", st)
	}
	res, err = b.CancelBisect()
	if err != nil || res.State.Profile != mainProfile || res.State.Bisect != nil || !strings.Contains(res.Message, "Поиск прерван") {
		t.Errorf("прерывание: %v, %q", err, res.Message)
	}
	if !sameTree(before, snapshot(t, g)) {
		t.Error("игра после прерванного поиска не такая, как до него")
	}

	// Один включённый мод — искать не из чего.
	b.CreateSet("Один", []string{"plain"})
	b.SwitchSet("Один")
	if _, err := b.StartBisect(); err == nil {
		t.Error("поиск среди одного мода начат")
	}
}

// После запуска игры программа подсказывает ответ по её журналу.
func TestBisectHint(t *testing.T) {
	a, _ := setsApp(t)
	logs := t.TempDir()
	a.game.(*darktide.Darktide).LogDir = logs
	path := filepath.Join(logs, "console-1.log")
	run := func(log string, at time.Time) *BisectStatus {
		t.Helper()
		if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, at, at)
		return state(t, a).Bisect
	}
	// Журнал запуска, который был до поиска, к шагу не относится.
	run("12:00:00.000 <<Crash>>старый сбой\n", time.Now().Add(-time.Hour))

	res, err := a.StartBisect()
	if err != nil {
		t.Fatal(err)
	}
	if st := res.State.Bisect; st.Ran || st.Suggest != "" || !strings.Contains(st.Hint, "ещё не запускалась") {
		t.Errorf("до запуска игры: %+v", st)
	}
	// Второй шаг: часть модов включена.
	if _, err = a.BisectAnswer(false); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(time.Hour)

	st := run("12:00:00.000 <<Crash>>Lua crash in hook\n<<Crash type>>lua<</Crash type>>\n", after)
	if !st.Ran || !st.Crashed || st.Suggest != "problem" || !strings.Contains(st.Hint, "закончился сбоем") || !strings.Contains(st.Hint, "Lua crash in hook") {
		t.Errorf("после сбоя: %+v", st)
	}
	// Нехватка памяти — не довод против модов этого шага.
	st = run("12:00:00.000 <<Crash>>Out of memory\n<<Crash type>>memory<</Crash type>>\n", after.Add(time.Minute))
	if !st.Crashed || st.Suggest != "" || !strings.Contains(st.Hint, "не хватило памяти") {
		t.Errorf("после нехватки памяти: %+v", st)
	}
	st = run("12:00:00.000 [Lua] всё хорошо\n", after.Add(2*time.Minute))
	if !st.Ran || st.Crashed || st.Suggest != "ok" || !strings.Contains(st.Hint, "сбоя после этого шага не было") {
		t.Errorf("после чистого запуска: %+v", st)
	}
	// Сбоя нет, но включённый мод выдал ошибки: ответ за пользователем.
	noisy := st.Testing[0]
	st = run("12:00:00.000 [Lua] [MOD]["+noisy+"][ERROR] hook failed\n", after.Add(3*time.Minute))
	if st.Crashed || st.Suggest != "" || !strings.Contains(st.Hint, "«"+noisy+"» — 1") {
		t.Errorf("после запуска с ошибками мода: %+v", st)
	}
	// Следующий шаг: журнал прошлого шага к нему не относится.
	if _, err = a.BisectAnswer(true); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, time.Now().Add(-time.Minute), time.Now().Add(-time.Minute))
	if st = state(t, a).Bisect; st != nil && st.Ran {
		t.Errorf("журнал прошлого шага принят за запуск этого: %+v", st)
	}
}
