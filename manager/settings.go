package manager

import (
	"strings"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/i18n"
)

// Настройки, которые окно показывает переключателями, памятка «Начало
// работы» и выбор победителя в конфликте файлов.

// Ключи настроек.
const (
	SettingCheckOnStart  = "checkOnStart"
	SettingDeployUpdates = "deployUpdates"
	SettingDirectLaunch  = "directLaunch"
	SettingNxm           = "nxm" // переключает окно: ему известен путь к программе
)

// Setting — настройка-переключатель.
type Setting struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	On     bool   `json:"on"`
}

// settingsList — настройки в том порядке, в каком их показывает окно.
func (a *Manager) settingsList() []Setting {
	list := []Setting{
		{
			Key: SettingCheckOnStart, Title: i18n.T("Проверять обновления при запуске"),
			Detail: i18n.T("Программа сама спрашивает Nexus о новых версиях, когда открывается окно. Выключено — только по кнопке «Проверить обновления»."),
			On:     a.settings.UpdateCheck != updateCheckManual,
		},
		{
			Key: SettingDeployUpdates, Title: i18n.T("Обновлённый мод сразу попадает в игру"),
			Detail: i18n.T("После обновления включённого мода новая версия сама ложится в игру. Выключено — по кнопке «Развернуть»."),
			On:     !a.settings.ManualUpdates,
		},
		{
			Key: SettingDirectLaunch, Title: i18n.T("Запускать игру без лаунчера"),
			Detail: i18n.T("«Играть» стартует саму игру, минуя окно лаунчера. Выключено — игра запускается через Steam, с лаунчером."),
			On:     !a.settings.LaunchViaLauncher,
		},
	}
	if owner, ours := a.nxmOwner(); owner != "" {
		detail := i18n.T("Кнопка «Mod Manager Download» на сайте Nexus ставит мод в Modvault.")
		if !ours {
			detail += i18n.T(" Сейчас: ") + nxmLabel(owner) + "."
		}
		list = append(list, Setting{Key: SettingNxm, Title: i18n.T("Открывать ссылки с сайта Nexus в Modvault"), Detail: detail, On: ours})
	}
	return list
}

// SetSetting включает или выключает настройку.
func (a *Manager) SetSetting(key string, on bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	switch key {
	case SettingCheckOnStart:
		a.settings.UpdateCheck = updateCheckManual
		if on {
			a.settings.UpdateCheck = ""
		}
	case SettingDeployUpdates:
		a.settings.ManualUpdates = !on
	case SettingDirectLaunch:
		a.settings.LaunchViaLauncher = !on
	default:
		return State{}, i18n.Errorf("неизвестная настройка %q", key)
	}
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// SetupStep — шаг памятки «Начало работы».
type SetupStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Done   bool   `json:"done"`
	// Action и Command — подпись и команда кнопки у невыполненного шага.
	Action  string `json:"action"`
	Command string `json:"command"`
}

// setupSteps — что осталось сделать, чтобы программа заработала в полную
// силу. Пусто, когда всё сделано или памятка скрыта.
func (a *Manager) setupSteps() []SetupStep {
	if a.settings.SetupHidden {
		return nil
	}
	steps := []SetupStep{{
		Title:  i18n.T("Папка игры"),
		Detail: i18n.T("Куда развёртывать моды."),
		Done:   a.settings.GameDir != "" && a.deployErr == nil,
		Action: i18n.T("Выбрать папку"), Command: "ChooseGame",
	}}
	rec, _ := a.loadRecord()
	vortex := false
	for _, m := range a.managers() {
		vortex = vortex || m == "Vortex"
	}
	if vortex || rec != nil {
		steps = append(steps, SetupStep{
			Title:  i18n.T("Перенять моды у Vortex"),
			Detail: i18n.T("Пока игрой управляет Vortex, Modvault в неё ничего не кладёт: ни обновления, ни наборы до игры не доходят. Файлы игры при этом не меняются, вернуть всё Vortex можно в любой момент."),
			Done:   rec != nil,
			Action: i18n.T("Перенять у Vortex"), Command: "Adopt",
		})
	}
	if a.game.NexusDomain() != "" {
		steps = append(steps, SetupStep{
			Title:  i18n.T("Ключ Nexus"),
			Detail: i18n.T("Нужен, чтобы проверять обновления и ставить моды кнопкой на сайте."),
			Done:   a.settings.NexusUser != "",
			Action: i18n.T("Ввести ключ"), Command: "NexusKey",
		})
		if owner, ours := a.nxmOwner(); owner != "" {
			steps = append(steps, SetupStep{
				Title:  i18n.T("Ссылки с сайта Nexus"),
				Detail: i18n.T("Чтобы кнопка «Mod Manager Download» ставила моды в Modvault."),
				Done:   ours,
				Action: i18n.T("Открывать в Modvault"), Command: "ToggleNxm",
			})
		}
	}
	for _, s := range steps {
		if !s.Done {
			return steps
		}
	}
	return nil
}

// HideSetup убирает памятку «Начало работы» насовсем.
func (a *Manager) HideSetup() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	a.settings.SetupHidden = true
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// Конфликт файлов: несколько модов кладут один и тот же файл. Обычно
// побеждает мод, стоящий ниже в порядке загрузки; победителя можно
// закрепить.

// conflictKey — ключ набора конфликтующих модов для замечания и команды.
func conflictKey(mods []string) string { return strings.Join(mods, "|") }

// WinnerOption — мод, который может победить в конфликте.
type WinnerOption struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"` // побеждает сейчас
	Default bool   `json:"default"` // побеждает без закрепления: стоит ниже всех
}

// WinnerChoice — выбор победителя в конфликте файлов.
type WinnerChoice struct {
	Files   int            `json:"files"`
	Options []WinnerOption `json:"options"`
}

// conflictsOf возвращает конфликты плана между модами key.
func conflictsOf(plan *deploy.Plan, key string) []deploy.Conflict {
	var out []deploy.Conflict
	for _, c := range plan.Conflicts {
		if conflictKey(c.Mods) == key {
			out = append(out, c)
		}
	}
	return out
}

// WinnerOptions перечисляет моды, из которых выбирают победителя.
func (a *Manager) WinnerOptions(key string) (WinnerChoice, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, err := a.plan()
	if err != nil {
		return WinnerChoice{}, err
	}
	conflicts := conflictsOf(plan, key)
	if len(conflicts) == 0 {
		return WinnerChoice{}, i18n.NewError("этого конфликта больше нет")
	}
	names := a.modNames()
	mods := conflicts[0].Mods
	choice := WinnerChoice{Files: len(conflicts)}
	for i, id := range mods {
		choice.Options = append(choice.Options, WinnerOption{
			ID: id, Name: names(id), Current: id == conflicts[0].Winner, Default: i == len(mods)-1,
		})
	}
	return choice, nil
}

// SetWinner закрепляет победителя во всех файлах конфликта между модами
// key. Выбор запоминается, даже если победитель и так стоял ниже всех:
// конфликт считается решённым. Снимает выбор UnpinWinner.
func (a *Manager) SetWinner(key, winner string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, err := a.plan()
	if err != nil {
		return State{}, err
	}
	conflicts := conflictsOf(plan, key)
	if len(conflicts) == 0 {
		return State{}, i18n.NewError("этого конфликта больше нет")
	}
	mods := conflicts[0].Mods
	known := false
	for _, id := range mods {
		known = known || id == winner
	}
	if !known {
		return State{}, i18n.Errorf("мод %q в этом конфликте не участвует", winner)
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	if p.Winners == nil {
		p.Winners = map[string]string{}
	}
	for _, c := range conflicts {
		// Прежнее закрепление этого файла могло быть записано в другом регистре.
		for path := range p.Winners {
			if strings.EqualFold(path, c.Path) {
				delete(p.Winners, path)
			}
		}
		// Запись остаётся и для победителя «по порядку загрузки»: это решение
		// пользователя, и конфликт больше не ждёт внимания.
		p.Winners[c.Path] = winner
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	a.note(EventSet, i18n.Sprintf("В конфликте файлов (%d) победителем выбран «%s»", len(conflicts), a.modName(winner)))
	return a.state()
}
