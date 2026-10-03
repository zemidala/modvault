package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/game"
)

// Поиск сбойного мода делением пополам. Игра падает или ведёт себя
// странно, а журнал виновника не называет: обычно его ищут, выключая моды
// по одному. Программа включает половину модов и спрашивает, осталась ли
// проблема; за log2(N) запусков игры от N подозреваемых остаётся один.
//
// Поиск идёт во временном наборе: свой набор пользователя не меняется, и
// по окончании программа возвращается к нему.
//
// Деление пополам верно, только если виновник один и проблема повторяется
// каждый раз. Поэтому мод, оставшийся последним, но ни разу не проверенный
// в одиночку, получает контрольный запуск: с ним одним проблема должна
// повториться. Не повторилась — виновник не назван.
//
// После каждого запуска игры программа читает её журнал и подсказывает
// ответ: был ли сбой и какие из включённых модов выдали ошибки.

const (
	// BisectSet — название временного набора, в котором идёт поиск.
	BisectSet  = "Поиск сбойного мода"
	bisectFile = "bisect.json"
)

// bisect — состояние поиска между запусками игры.
type bisect struct {
	Origin   string   `json:"origin"`   // набор, с которого начали и к которому вернёмся
	Suspects []string `json:"suspects"` // моды, среди которых ищем
	Testing  []string `json:"testing"`  // подозреваемые, включённые в этом шаге
	Step     int      `json:"step"`
	// Since — когда игра приведена к этому шагу: журнал игры новее этого
	// времени относится к нему.
	Since time.Time `json:"since"`
	// Confirm — контрольный запуск: включён один последний подозреваемый.
	Confirm bool `json:"confirm,omitempty"`
}

// BisectStatus — ход поиска для окна.
type BisectStatus struct {
	Step     int      `json:"step"`
	Steps    int      `json:"steps"`    // сколько шагов понадобится самое большее
	Suspects int      `json:"suspects"` // сколько модов под подозрением
	Testing  []string `json:"testing"`  // названия подозреваемых, включённых сейчас
	Origin   string   `json:"origin"`
	// Confirm — идёт контрольный запуск с одним последним подозреваемым.
	Confirm bool `json:"confirm"`
	// Ran — игра запускалась после этого шага; Crashed — и упала.
	Ran     bool `json:"ran"`
	Crashed bool `json:"crashed"`
	// Hint — что о запуске говорит журнал игры. Suggest — ответ, на который
	// он указывает: "problem", "ok" или пусто, если журнал не помощник.
	Hint    string `json:"hint"`
	Suggest string `json:"suggest"`
}

// BisectResult — итог шага поиска.
type BisectResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
	// Culprit и CulpritName — найденный мод; пусто, пока поиск идёт или
	// если он прерван.
	Culprit     string `json:"culprit"`
	CulpritName string `json:"culpritName"`
	// Done — поиск закончен. Без Culprit это значит, что одного виновника
	// назвать не удалось; почему — в Message.
	Done bool `json:"done"`
}

func (a *Manager) bisectPath() string { return filepath.Join(a.home, bisectFile) }

func (a *Manager) loadBisect() (*bisect, error) {
	data, err := os.ReadFile(a.bisectPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var b bisect
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", bisectFile, err)
	}
	return &b, nil
}

func (a *Manager) saveBisect(b *bisect) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(a.bisectPath(), data)
}

// bisectStatus возвращает ход поиска, если он идёт: открыт временный набор.
func (a *Manager) bisectStatus() *BisectStatus {
	if a.profileName() != BisectSet {
		return nil
	}
	b, err := a.loadBisect()
	if err != nil || b == nil {
		return nil
	}
	names := a.modNames()
	st := &BisectStatus{Step: b.Step, Suspects: len(b.Suspects), Origin: b.Origin, Testing: []string{}, Confirm: b.Confirm}
	for _, id := range b.Testing {
		st.Testing = append(st.Testing, names(id))
	}
	// Сколько шагов понадобится самое большее: пока подозреваемых больше
	// одного, и ещё контрольный запуск.
	st.Steps = b.Step
	if !b.Confirm {
		for n := len(b.Suspects); n > 1; n = (n + 1) / 2 {
			st.Steps++
		}
	}
	a.bisectHint(b, st)
	return st
}

// errBisecting — пока идёт поиск, набором модов распоряжается он: ручная
// правка сделала бы ответы пользователя ответами о других модах.
var errBisecting = errors.New("идёт поиск сбойного мода: сначала закончите или прервите его")

// bisecting возвращает errBisecting, если поиск идёт.
func (a *Manager) bisecting() error {
	if a.profileName() == BisectSet {
		return errBisecting
	}
	return nil
}

// BisectStatus возвращает ход поиска с подсказкой по журналу игры; nil —
// поиск не идёт. Окно спрашивает его, пока пользователь в игре: полное
// состояние для этого пересчитывать незачем.
func (a *Manager) BisectStatus() *BisectStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bisectStatus()
}

// bisectHint читает журнал игры и подсказывает ответ на шаг поиска. Решает
// всё равно пользователь: искать можно не только сбой, а журнал пишется и
// пока игра ещё идёт.
func (a *Manager) bisectHint(b *bisect, st *BisectStatus) {
	rep, ok := a.game.LastRun()
	if b.Since.IsZero() || !ok || !rep.Time.After(b.Since) {
		st.Hint = "После этого шага игра ещё не запускалась"
		return
	}
	st.Ran, st.Crashed = true, rep.Crashed
	if rep.Crashed {
		st.Hint = "По журналу игры запуск после этого шага закончился сбоем. " + crashReason(rep)
		// Нехватка памяти от набора модов не зависит: ответ не подсказываем.
		if !strings.EqualFold(rep.CrashKind, "memory") {
			st.Suggest = "problem"
		}
		return
	}
	st.Hint = "По журналу игры сбоя после этого шага не было"
	p, _, err := a.loadProfile()
	if err != nil {
		return
	}
	troubles, _ := a.runDiagnosis(p)
	if len(troubles) == 0 {
		st.Hint += ", ошибок включённых модов тоже нет"
		st.Suggest = "ok"
		return
	}
	names := a.modNames()
	var noisy []string
	for id, t := range troubles {
		noisy = append(noisy, fmt.Sprintf("«%s» — %d", names(id), t.count))
	}
	sort.Strings(noisy)
	st.Hint += ", но моды выдали ошибки: " + strings.Join(noisy, ", ")
}

// StartBisect начинает поиск среди включённых модов текущего набора.
func (a *Manager) StartBisect() (BisectResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deployer == nil || a.deployErr != nil {
		return BisectResult{}, errors.Join(errors.New("сначала выберите папку игры"), a.deployErr)
	}
	if managers := a.managers(); len(managers) > 0 {
		return BisectResult{}, fmt.Errorf("игрой управляет %s: Modvault не может включать и выключать моды в игре", strings.Join(managers, " и "))
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return BisectResult{}, err
	}
	if p.Name == BisectSet {
		return BisectResult{}, errors.New("поиск уже идёт: ответьте, осталась ли проблема, или прервите его")
	}
	var suspects []string
	for _, e := range p.Entries {
		if !e.Enabled {
			continue
		}
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return BisectResult{}, err
		}
		// Загрузчик и фреймворк нужны всем: без них не проверить ничего.
		if l, err := a.layout(v); err == nil && l.Role == game.RoleNone {
			suspects = append(suspects, e.ModID)
		}
	}
	if len(suspects) < 2 {
		return BisectResult{}, errors.New("в наборе меньше двух включённых модов: искать не из чего")
	}
	if err := a.profiles.Save(p); err != nil { // к этому набору вернёмся в конце
		return BisectResult{}, err
	}
	a.note(EventSet, fmt.Sprintf("Начат поиск сбойного мода в наборе «%s»: подозреваемых %d", p.Name, len(suspects)))
	return a.bisectStep(&bisect{Origin: p.Name, Suspects: suspects})
}

// half включает во временном наборе часть подозреваемых и то, без чего
// они не заработают. Возвращает набор и подозреваемых, оказавшихся включёнными.
func (a *Manager) half(origin profile.Profile, suspects, enable []string) (profile.Profile, []string, error) {
	next := profile.Profile{Name: BisectSet, Entries: append([]profile.Entry(nil), origin.Entries...), Winners: origin.Winners}
	for i := range next.Entries {
		next.Entries[i].Enabled = false
	}
	if _, err := a.withRequired(&next, enable); err != nil {
		return profile.Profile{}, nil, err
	}
	suspect := map[string]bool{}
	for _, id := range suspects {
		suspect[id] = true
	}
	var tested []string
	for _, e := range next.Entries {
		if e.Enabled && suspect[e.ModID] {
			tested = append(tested, e.ModID)
		}
	}
	return next, tested, nil
}

// bisectStep включает очередную половину подозреваемых и приводит к ней игру.
func (a *Manager) bisectStep(b *bisect) (BisectResult, error) {
	origin, err := a.profiles.Load(b.Origin)
	if err != nil {
		return BisectResult{}, fmt.Errorf("набор «%s», с которого начат поиск, не найден", b.Origin)
	}
	mid := (len(b.Suspects) + 1) / 2
	next, tested, err := a.half(origin, b.Suspects, b.Suspects[:mid])
	if err != nil {
		return BisectResult{}, err
	}
	if len(tested) == len(b.Suspects) {
		// Первая половина тянет за собой всех остальных: пробуем вторую.
		if next, tested, err = a.half(origin, b.Suspects, b.Suspects[mid:]); err != nil {
			return BisectResult{}, err
		}
	}
	if len(tested) == len(b.Suspects) {
		// Моды зависят друг от друга по кругу: разделить их нельзя.
		return a.finishBisect(b, "")
	}
	msg := fmt.Sprintf("Шаг %d: включено %d из %d подозреваемых. Запустите игру, проверьте и ответьте, осталась ли проблема",
		b.Step+1, len(tested), len(b.Suspects))
	return a.bisectDeploy(b, next, tested, msg)
}

// confirmStep — контрольный запуск: в игре остаётся один последний
// подозреваемый и то, без чего он не заработает.
func (a *Manager) confirmStep(b *bisect) (BisectResult, error) {
	origin, err := a.profiles.Load(b.Origin)
	if err != nil {
		return BisectResult{}, fmt.Errorf("набор «%s», с которого начат поиск, не найден", b.Origin)
	}
	next, tested, err := a.half(origin, b.Suspects, b.Suspects)
	if err != nil {
		return BisectResult{}, err
	}
	b.Confirm = true
	msg := fmt.Sprintf("Шаг %d, контрольный: остался «%s», в игре включён только он. Запустите игру и ответьте, осталась ли проблема",
		b.Step+1, a.modNames()(b.Suspects[0]))
	return a.bisectDeploy(b, next, tested, msg)
}

// bisectDeploy записывает шаг поиска и приводит игру к его набору модов.
func (a *Manager) bisectDeploy(b *bisect, next profile.Profile, tested []string, msg string) (BisectResult, error) {
	// Шаг записывается до развёртывания: если оно сорвётся, набор и запись
	// о шаге всё равно говорят об одном и том же.
	b.Testing, b.Step, b.Since = tested, b.Step+1, time.Time{}
	if err := a.saveBisect(b); err != nil {
		return BisectResult{}, err
	}
	if err := a.profiles.Save(next); err != nil {
		return BisectResult{}, err
	}
	res, err := a.switchSet(BisectSet)
	if err != nil {
		return BisectResult{}, err
	}
	// Время шага — после развёртывания: журнал запуска, шедшего до него,
	// к этому шагу не относится.
	b.Since = time.Now()
	if err := a.saveBisect(b); err != nil {
		return BisectResult{}, err
	}
	res.State.Bisect = a.bisectStatus()
	return BisectResult{State: res.State, Message: msg}, nil
}

// BisectAnswer принимает ответ пользователя после запуска игры: осталась
// ли проблема с модами, включёнными в этом шаге.
func (a *Manager) BisectAnswer(problem bool) (BisectResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := a.loadBisect()
	if err != nil {
		return BisectResult{}, err
	}
	if b == nil || a.profileName() != BisectSet {
		return BisectResult{}, errors.New("поиск сбойного мода не идёт")
	}
	if b.Confirm {
		if problem {
			return a.finishBisect(b, b.Suspects[0])
		}
		return a.finishUnconfirmed(b)
	}
	// Проблема осталась с единственным включённым подозреваемым — он уже
	// проверен в одиночку, контрольный запуск не нужен.
	alone := problem && len(b.Testing) == 1
	if problem {
		b.Suspects = b.Testing
	} else {
		tested := map[string]bool{}
		for _, id := range b.Testing {
			tested[id] = true
		}
		var rest []string
		for _, id := range b.Suspects {
			if !tested[id] {
				rest = append(rest, id)
			}
		}
		b.Suspects = rest
	}
	switch {
	case len(b.Suspects) == 0:
		return a.finishBisect(b, "")
	case len(b.Suspects) == 1 && alone:
		return a.finishBisect(b, b.Suspects[0])
	case len(b.Suspects) == 1:
		return a.confirmStep(b)
	}
	return a.bisectStep(b)
}

// CancelBisect прерывает поиск и возвращает прежний набор.
func (a *Manager) CancelBisect() (BisectResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := a.loadBisect()
	if err != nil {
		return BisectResult{}, err
	}
	if b == nil {
		return BisectResult{}, errors.New("поиск сбойного мода не идёт")
	}
	res, err := a.endBisect(b)
	res.Message = fmt.Sprintf("Поиск прерван. Возвращён набор «%s»", b.Origin)
	a.note(EventSet, res.Message)
	return res, err
}

// endBisect возвращает игру к исходному набору и убирает следы поиска.
func (a *Manager) endBisect(b *bisect) (BisectResult, error) {
	res, err := a.switchSet(b.Origin)
	if err != nil {
		return BisectResult{}, err
	}
	a.profiles.Delete(BisectSet)
	os.Remove(a.bisectPath())
	st, err := a.state() // временного набора в списках модов больше нет
	return BisectResult{State: st, Message: res.Message, Done: true}, err
}

// finishUnconfirmed завершает поиск без виновника: последний подозреваемый
// в одиночку проблему не вызвал.
func (a *Manager) finishUnconfirmed(b *bisect) (BisectResult, error) {
	name := a.modNames()(b.Suspects[0])
	res, err := a.endBisect(b)
	if err != nil {
		return BisectResult{}, err
	}
	res.Message = fmt.Sprintf("Одного виновника назвать не удалось: последним под подозрением остался «%s», но с ним одним проблемы нет. "+
		"Значит, её вызывает сочетание модов или она повторяется не каждый раз. Возвращён набор «%s»", name, b.Origin)
	a.note(EventSet, res.Message)
	return res, nil
}

// finishBisect завершает поиск: culprit — найденный мод; пусто — сузить
// круг до одного мода не удалось.
func (a *Manager) finishBisect(b *bisect, culprit string) (BisectResult, error) {
	names := a.modNames()
	res, err := a.endBisect(b)
	if err != nil {
		return BisectResult{}, err
	}
	switch {
	case culprit == "" && len(b.Suspects) == 0:
		res.Message = fmt.Sprintf("Подозреваемых не осталось: виновника среди модов набора нет. Возвращён набор «%s»", b.Origin)
	case culprit == "":
		list := make([]string, len(b.Suspects))
		for i, id := range b.Suspects {
			list[i] = "«" + names(id) + "»"
		}
		res.Message = fmt.Sprintf("Круг сузился до %d %s, которые зависят друг от друга и по отдельности не включаются: %s. Возвращён набор «%s»",
			len(list), plural(len(list), "мода", "модов", "модов"), strings.Join(list, ", "), b.Origin)
	default:
		res.Culprit, res.CulpritName = culprit, names(culprit)
		res.Message = fmt.Sprintf("Найден сбойный мод: «%s» (шагов: %d). Возвращён набор «%s»", res.CulpritName, b.Step, b.Origin)
	}
	a.note(EventSet, res.Message)
	return res, nil
}
