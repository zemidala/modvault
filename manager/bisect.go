package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
}

// BisectStatus — ход поиска для окна.
type BisectStatus struct {
	Step     int      `json:"step"`
	Steps    int      `json:"steps"`    // сколько шагов понадобится самое большее
	Suspects int      `json:"suspects"` // сколько модов под подозрением
	Testing  []string `json:"testing"`  // названия подозреваемых, включённых сейчас
	Origin   string   `json:"origin"`
}

// BisectResult — итог шага поиска.
type BisectResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
	// Culprit и CulpritName — найденный мод; пусто, пока поиск идёт или
	// если он прерван.
	Culprit     string `json:"culprit"`
	CulpritName string `json:"culpritName"`
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
	st := &BisectStatus{Step: b.Step, Suspects: len(b.Suspects), Origin: b.Origin, Testing: []string{}}
	for _, id := range b.Testing {
		st.Testing = append(st.Testing, names(id))
	}
	// Сколько ещё шагов самое большее: пока подозреваемых больше одного.
	st.Steps = b.Step - 1
	for n := len(b.Suspects); n > 1; n = (n + 1) / 2 {
		st.Steps++
	}
	return st
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
	b.Testing, b.Step = tested, b.Step+1
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
	msg := fmt.Sprintf("Шаг %d: включено %d из %d подозреваемых. Запустите игру, проверьте и ответьте, осталась ли проблема",
		b.Step, len(tested), len(b.Suspects))
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
	if len(b.Suspects) == 1 {
		return a.finishBisect(b, b.Suspects[0])
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
	return BisectResult{State: st, Message: res.Message}, err
}

// finishBisect завершает поиск: culprit — найденный мод; пусто — сузить
// круг до одного мода не удалось.
func (a *Manager) finishBisect(b *bisect, culprit string) (BisectResult, error) {
	names := a.modNames()
	res, err := a.endBisect(b)
	if err != nil {
		return BisectResult{}, err
	}
	if culprit == "" {
		list := make([]string, len(b.Suspects))
		for i, id := range b.Suspects {
			list[i] = "«" + names(id) + "»"
		}
		res.Message = fmt.Sprintf("Круг сузился до %d %s, которые зависят друг от друга и по отдельности не включаются: %s. Возвращён набор «%s»",
			len(list), plural(len(list), "мода", "модов", "модов"), strings.Join(list, ", "), b.Origin)
	} else {
		res.Culprit, res.CulpritName = culprit, names(culprit)
		res.Message = fmt.Sprintf("Найден сбойный мод: «%s» (шагов: %d). Возвращён набор «%s»", res.CulpritName, b.Step, b.Origin)
	}
	a.note(EventSet, res.Message)
	return res, nil
}
