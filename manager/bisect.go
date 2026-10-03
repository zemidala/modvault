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
	"github.com/zemidala/modvault/rules"
)

// Поиск сбойного мода. Игра падает или ведёт себя странно, а журнал
// виновника не называет: обычно его ищут, выключая моды по одному.
// Программа включает часть модов и спрашивает, осталась ли проблема.
//
// Ищется наименьший набор модов, с которым проблема повторяется, — один мод
// или сочетание нескольких:
//
//  1. Сначала в игре остаются только найденные виновники (в первый раз —
//     ни одного мода, только загрузчик и фреймворк). Проблема есть — поиск
//     закончен: без модов это значит, что дело не в них.
//  2. Иначе делением пополам ищется самое короткое начало списка
//     подозреваемых, с которым (вместе с уже найденными) проблема
//     повторяется. Последний мод этого начала — виновник; искать дальше
//     остаётся только среди модов перед ним. Затем снова шаг 1.
//
// На одного виновника среди N модов уходит log2(N) запусков игры и ещё
// один-два проверочных; на каждого следующего в сочетании — ещё столько же.
// Виновник называется, только когда проблема повторилась с ним одним (или
// с ними одними): ошибочный ответ или непостоянная проблема к обвинению
// невиновного не приведут.
//
// Поиск идёт во временном наборе: свой набор пользователя не меняется, и
// по окончании программа возвращается к нему.
//
// После каждого запуска игры программа читает её журнал и подсказывает
// ответ: был ли сбой и какие из включённых модов выдали ошибки.

const (
	// BisectSet — название временного набора, в котором идёт поиск.
	BisectSet  = "Поиск сбойного мода"
	bisectFile = "bisect.json"
)

// Что проверяет шаг поиска.
const (
	bisectBase   = "base"   // в игре только найденные виновники
	bisectVerify = "verify" // в игре все моды: повторяется ли проблема вообще
	bisectSearch = "search" // найденные и начало списка подозреваемых
)

// Чем закончился поиск.
const (
	outcomeFound    = "found"    // виновники названы
	outcomeNotMods  = "notmods"  // проблема осталась без модов
	outcomeUnstable = "unstable" // проблема повторяется не каждый раз
)

// bisect — состояние поиска между запусками игры.
type bisect struct {
	Origin string `json:"origin"` // набор, с которого начали и к которому вернёмся
	// Suspects — все моды, среди которых ищем; мод стоит после тех, кого он
	// требует, так что начало списка включается без модов из его конца.
	Suspects []string `json:"suspects"`
	// Found — виновники, найденные к этому шагу: без каждого из них
	// проблема не повторялась.
	Found []string `json:"found"`
	// Pool — моды, среди которых ищется следующий виновник. С началом Pool
	// длиной Lo (и с Found) проблемы нет, с началом длиной Hi — есть.
	Pool []string `json:"pool"`
	Lo   int      `json:"lo"`
	Hi   int      `json:"hi"`
	Kind string   `json:"kind"`
	// Proven — проблема хоть раз повторилась на глазах у поиска. Пока это
	// не так, о ней известно только со слов пользователя.
	Proven  bool     `json:"proven"`
	Testing []string `json:"testing"` // подозреваемые, включённые в этом шаге
	Step    int      `json:"step"`
	// Since — когда игра приведена к этому шагу: журнал игры новее этого
	// времени относится к нему.
	Since time.Time `json:"since"`
	// Known — ответы на уже проверенные составы модов: один и тот же состав
	// дважды не запускается.
	Known map[string]bool `json:"known"`
}

// BisectStatus — ход поиска для окна.
type BisectStatus struct {
	Step     int      `json:"step"`
	Steps    int      `json:"steps"`    // сколько шагов понадобится, если виновник один
	Suspects int      `json:"suspects"` // сколько модов под подозрением
	Testing  []string `json:"testing"`  // названия подозреваемых, включённых сейчас
	Found    []string `json:"found"`    // названия уже найденных виновников
	Origin   string   `json:"origin"`
	// Kind — что проверяет шаг: "base" — в игре только найденные виновники
	// (или ни одного мода), "verify" — все моды, "search" — часть модов.
	Kind string `json:"kind"`
	// Ran — игра запускалась после этого шага; Crashed — и упала.
	Ran     bool `json:"ran"`
	Crashed bool `json:"crashed"`
	// Hint — что о запуске говорит журнал игры. Suggest — ответ, на который
	// он указывает: "problem", "ok" или пусто, если журнал не помощник.
	Hint    string `json:"hint"`
	Suggest string `json:"suggest"`
}

// Culprit — мод, найденный поиском.
type Culprit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BisectResult — итог шага поиска.
type BisectResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
	// Done — поиск закончен; чем — в Message.
	Done bool `json:"done"`
	// Culprits — найденные моды: один или сочетание, которое вызывает
	// проблему только вместе. Пусто, пока поиск идёт, если он прерван или
	// виновника нет.
	Culprits []Culprit `json:"culprits"`
	// Culprit и CulpritName — найденный мод, когда он один.
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
	if b.Known == nil {
		b.Known = map[string]bool{}
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

// stepsLeft — сколько ещё запусков игры нужно самое большее, если виновник
// один: деление пополам и проверка найденного.
func (b *bisect) stepsLeft() int {
	n := 0
	switch b.Kind {
	case bisectSearch:
		for span := b.Hi - b.Lo; span > 1; span = (span + 1) / 2 {
			n++
		}
		return n // этот шаг уже считан в делении; проверка найденного — ещё один
	default:
		for span := len(b.Pool); span > 1; span = (span + 1) / 2 {
			n++
		}
		return n + 1
	}
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
	st := &BisectStatus{
		Step: b.Step, Steps: b.Step + b.stepsLeft(), Suspects: len(b.Pool), Origin: b.Origin, Kind: b.Kind,
		Testing: []string{}, Found: []string{},
	}
	for _, id := range b.Testing {
		st.Testing = append(st.Testing, names(id))
	}
	for _, id := range b.Found {
		st.Found = append(st.Found, names(id))
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
	if suspects, err = a.requiredFirst(p, suspects); err != nil {
		return BisectResult{}, err
	}
	if err := a.profiles.Save(p); err != nil { // к этому набору вернёмся в конце
		return BisectResult{}, err
	}
	a.note(EventSet, fmt.Sprintf("Начат поиск сбойного мода в наборе «%s»: подозреваемых %d", p.Name, len(suspects)))
	return a.bisectNext(&bisect{Origin: p.Name, Suspects: suspects, Pool: suspects, Kind: bisectBase, Known: map[string]bool{}})
}

// requiredFirst ставит подозреваемых так, чтобы мод шёл после тех, кого он
// требует. Тогда начало списка включается без модов из его конца, и мод, на
// котором проблема появляется, — сам виновник, а не тот, кто его требует.
func (a *Manager) requiredFirst(p profile.Profile, suspects []string) ([]string, error) {
	mods, ord, err := a.orderInfo(p)
	if err != nil {
		return nil, err
	}
	owner := map[string]string{} // папка → мод
	for _, m := range mods {
		for _, f := range m.folders {
			owner[strings.ToLower(f)] = m.entry.ModID
		}
	}
	needs := map[string][]string{}
	for _, r := range ord.Rules {
		if r.Kind != rules.Requires {
			continue
		}
		mod, okMod := owner[strings.ToLower(r.Mod)]
		other, okOther := owner[strings.ToLower(r.Other)]
		if okMod && okOther && mod != other {
			needs[mod] = append(needs[mod], other)
		}
	}
	// Чем больше модов нужно моду (прямо и через других), тем он дальше.
	depth := map[string]int{}
	for _, id := range suspects {
		seen := map[string]bool{id: true}
		for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
			for _, need := range needs[queue[0]] {
				if !seen[need] {
					seen[need] = true
					queue = append(queue, need)
				}
			}
		}
		depth[id] = len(seen)
	}
	out := append([]string(nil), suspects...)
	sort.SliceStable(out, func(i, j int) bool { return depth[out[i]] < depth[out[j]] })
	return out, nil
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

// testKey — запись состава включённых подозреваемых для Known.
func testKey(tested []string) string {
	sorted := append([]string(nil), tested...)
	sort.Strings(sorted)
	return strings.Join(sorted, "|")
}

// bisectNext готовит следующий запуск игры. Состав модов, ответ о котором
// уже известен, не запускается: поиск сразу идёт дальше.
func (a *Manager) bisectNext(b *bisect) (BisectResult, error) {
	origin, err := a.profiles.Load(b.Origin)
	if err != nil {
		return BisectResult{}, fmt.Errorf("набор «%s», с которого начат поиск, не найден", b.Origin)
	}
	names := a.modNames()
	for {
		var enable []string
		var msg string
		switch b.Kind {
		case bisectBase:
			enable = b.Found
			msg = "моды набора выключены, в игре только загрузчик модов и фреймворк"
			if len(b.Found) > 0 {
				msg = "контрольный — включены только " + quoteNames(names, b.Found)
			}
		case bisectVerify:
			enable = b.Suspects
			msg = "включены все моды набора — проверка, что проблема повторяется"
		default:
			if b.Hi-b.Lo <= 1 && !b.Proven {
				// Проблема ни разу не повторилась на глазах у поиска, и
				// последний мод выходит виновником только со слов: сначала
				// убеждаемся, что с полным набором она есть.
				b.Kind = bisectVerify
				continue
			}
			if b.Hi-b.Lo <= 1 {
				// С началом длиной Lo проблемы нет, с началом на один мод
				// длиннее — есть: этот мод — виновник. Следующего искать
				// остаётся только среди модов перед ним.
				b.Found = append(b.Found, b.Pool[b.Hi-1])
				b.Pool = b.Pool[:b.Hi-1]
				b.Kind = bisectBase
				continue
			}
			mid := (b.Lo + b.Hi) / 2
			enable = append(append([]string(nil), b.Found...), b.Pool[:mid]...)
			msg = fmt.Sprintf("включено %d из %d подозреваемых", mid, len(b.Pool))
		}
		next, tested, err := a.half(origin, b.Suspects, enable)
		if err != nil {
			return BisectResult{}, err
		}
		if problem, known := b.Known[testKey(tested)]; known {
			if outcome := b.answer(problem); outcome != "" {
				return a.finishBisect(b, outcome)
			}
			continue
		}
		msg = fmt.Sprintf("Шаг %d: %s. Запустите игру, проверьте и ответьте, осталась ли проблема", b.Step+1, msg)
		return a.bisectDeploy(b, next, tested, msg)
	}
}

// answer учитывает ответ на шаг: осталась ли проблема. Возвращает исход,
// если поиск на этом закончен.
func (b *bisect) answer(problem bool) string {
	b.Proven = b.Proven || problem
	switch b.Kind {
	case bisectBase:
		switch {
		case problem && len(b.Found) == 0:
			return outcomeNotMods
		case problem:
			return outcomeFound
		case len(b.Pool) == 0:
			// Все подозреваемые перебраны, а с найденными проблемы нет:
			// ответы противоречат друг другу.
			return outcomeUnstable
		default:
			b.Kind, b.Lo, b.Hi = bisectSearch, 0, len(b.Pool)
		}
	case bisectVerify:
		if !problem {
			return outcomeUnstable
		}
		b.Kind = bisectSearch // границы деления остаются прежними
	default:
		if mid := (b.Lo + b.Hi) / 2; problem {
			b.Hi = mid
		} else {
			b.Lo = mid
		}
	}
	return ""
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
	b.Known[testKey(b.Testing)] = problem
	if outcome := b.answer(problem); outcome != "" {
		return a.finishBisect(b, outcome)
	}
	return a.bisectNext(b)
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
	res.Done = false // прерван, а не закончен: итога нет
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
	return BisectResult{State: st, Message: res.Message, Done: true, Culprits: []Culprit{}}, err
}

// quoteNames перечисляет названия модов в кавычках.
func quoteNames(names func(string) string, ids []string) string {
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = "«" + names(id) + "»"
	}
	return strings.Join(list, ", ")
}

// finishBisect завершает поиск с исходом outcome и возвращает прежний набор.
func (a *Manager) finishBisect(b *bisect, outcome string) (BisectResult, error) {
	names := a.modNames()
	res, err := a.endBisect(b)
	if err != nil {
		return BisectResult{}, err
	}
	switch outcome {
	case outcomeNotMods:
		res.Message = "Проблема осталась и без модов набора: в игре были только загрузчик модов и фреймворк. " +
			"Дело не в модах набора — в самой игре, загрузчике или фреймворке."
	case outcomeUnstable:
		res.Message = "Проблема повторяется не каждый раз: с одним и тем же составом модов она то есть, то нет. " +
			"Делением такую не найти — поиск остановлен, никто не обвинён."
		if !b.Proven {
			res.Message = "С полным набором модов проблема не повторилась. Она проявляется не каждый раз, " +
				"и делением такую не найти — поиск остановлен, никто не обвинён."
		}
	default:
		for _, id := range b.Found {
			res.Culprits = append(res.Culprits, Culprit{ID: id, Name: names(id)})
		}
		if len(b.Found) == 1 {
			res.Culprit, res.CulpritName = b.Found[0], names(b.Found[0])
			res.Message = fmt.Sprintf("Найден сбойный мод: «%s» (шагов: %d).", res.CulpritName, b.Step)
		} else {
			res.Message = fmt.Sprintf("Проблему вызывает сочетание модов: %s (шагов: %d). Она повторяется, когда включены только они, и пропадает без любого из них.",
				quoteNames(names, b.Found), b.Step)
		}
	}
	res.Message += fmt.Sprintf(" Возвращён набор «%s»", b.Origin)
	a.note(EventSet, res.Message)
	return res, nil
}
