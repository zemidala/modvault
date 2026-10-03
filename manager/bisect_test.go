package manager

import (
	"fmt"
	"strings"
	"testing"
)

// hunt ведёт поиск до конца, отвечая за игру: проблема есть, если сбойный
// мод включён. Возвращает итог и число запусков игры.
func hunt(t *testing.T, a *Manager, culprit string) (BisectResult, int) {
	t.Helper()
	res, err := a.StartBisect()
	if err != nil {
		t.Fatal(err)
	}
	for runs := 1; runs < 20; runs++ {
		if res.State.Profile != BisectSet || res.State.Bisect == nil {
			t.Fatalf("шаг %d: поиск не показан окну: набор %q, ход %+v", runs, res.State.Profile, res.State.Bisect)
		}
		if res.State.PlanTitle != "" {
			t.Fatalf("шаг %d: игра не приведена к половине: %q", runs, res.State.PlanTitle)
		}
		if res, err = a.BisectAnswer(findMod(t, res.State, culprit).Enabled); err != nil {
			t.Fatal(err)
		}
		if res.State.Profile != BisectSet {
			return res, runs
		}
	}
	t.Fatal("поиск не закончился")
	return BisectResult{}, 0
}

func TestBisect(t *testing.T) {
	// Пять подозреваемых: needy → helper → deep (зависимости), plain, other.
	for _, culprit := range []string{"needy", "helper", "deep", "plain", "other"} {
		a, g := setsApp(t)
		before := snapshot(t, g)
		all := enabledIDs(state(t, a))

		res, runs := hunt(t, a, culprit)
		if res.Culprit != culprit || !strings.Contains(res.Message, "Найден сбойный мод") {
			t.Errorf("%s: найден %q за %d запусков: %s", culprit, res.Culprit, runs, res.Message)
		}
		if runs > 3 {
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
	if res.Culprit != "mod27" || runs > 6 {
		t.Errorf("найден %q за %d запусков игры: %s", res.Culprit, runs, res.Message)
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
	if st := res.State.Bisect; st.Step != 1 || st.Suspects != 5 || st.Steps != 3 || st.Origin != mainProfile || len(st.Testing) == 0 {
		t.Errorf("ход поиска: %+v", st)
	}
	if _, err := a.StartBisect(); err == nil {
		t.Error("второй поиск поверх первого начат")
	}
	// Поиск переживает перезапуск программы.
	b := NewWith(a.home, a.game)
	if st := state(t, b).Bisect; st == nil || st.Step != 1 {
		t.Fatalf("после перезапуска: %+v", st)
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
