package manager

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/i18n"
)

func languageSetting(t *testing.T, s State) Setting {
	t.Helper()
	for _, item := range s.Settings {
		if item.Key == SettingEnglish {
			return item
		}
	}
	t.Fatal("настройки языка нет")
	return Setting{}
}

// Язык выбирается в настройках и вступает в силу со следующего запуска.
func TestLanguageSetting(t *testing.T) {
	i18n.UseFile(filepath.Join(t.TempDir(), "language"))
	defer i18n.UseFile("")
	a, _ := setsApp(t)

	item := languageSetting(t, state(t, a))
	if item.On || !strings.Contains(item.Title, "English") || !strings.Contains(item.Title, "Английский") {
		t.Errorf("до выбора: %+v", item)
	}
	s, err := a.SetSetting(SettingEnglish, true)
	if err != nil {
		t.Fatal(err)
	}
	// Выбор запомнен, но этот запуск остаётся русским — и говорит об этом.
	if item = languageSetting(t, s); !item.On || i18n.Saved() != i18n.English || i18n.Language() != i18n.Russian {
		t.Errorf("после выбора: %+v, сохранён %q, идёт %q", item, i18n.Saved(), i18n.Language())
	}
	if !strings.Contains(item.Detail, "Перезапустите программу") {
		t.Errorf("подсказка о перезапуске: %q", item.Detail)
	}
	if s, _ = a.SetSetting(SettingEnglish, false); languageSetting(t, s).On || i18n.Saved() != i18n.Russian {
		t.Error("язык не вернулся к русскому")
	}
}

// С английским языком программа говорит по-английски: сообщения, формы
// слов, кавычки — и ни одной русской буквы в том, что она составляет сама.
func TestEnglishMessages(t *testing.T) {
	i18n.Use(i18n.English)
	defer i18n.Use(i18n.Russian)
	a, _ := setsApp(t)

	res, err := a.MoveMods([]string{"needy"}, "plain", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "“Needy” is now placed after “Plain”") {
		t.Errorf("сообщение о перестановке: %q", res.Message)
	}
	if _, err := a.SetEnabled("нет такого", true); err == nil || strings.ContainsAny(strings.ReplaceAll(err.Error(), "нет такого", ""), "абвгдежзийклмнопрстуфхцчшщъыьэюя") {
		t.Errorf("ошибка по-английски: %v", err)
	}
	set, err := a.CreateSet("Trial", []string{"plain", "other"})
	if err != nil || !strings.Contains(set.Message, "2 mods") {
		t.Errorf("формы слова: %q, %v", set.Message, err)
	}

	s := state(t, a)
	russian := func(text string) bool {
		// Название набора — данные пользователя, а не текст программы.
		text = strings.ReplaceAll(text, mainProfile, "")
		for _, r := range text {
			if r >= 'А' && r <= 'я' || r == 'ё' || r == 'Ё' {
				return true
			}
		}
		return false
	}
	for _, item := range s.Status {
		if russian(item.Label + item.Value) {
			t.Errorf("строка состояния по-русски: %+v", item)
		}
	}
	for _, issue := range s.Issues {
		if russian(issue.Title + issue.Detail + issue.Action) {
			t.Errorf("замечание по-русски: %+v", issue)
		}
	}
	for _, m := range s.Mods {
		if russian(m.State + m.Source) {
			t.Errorf("мод по-русски: %s — %q, %q", m.ID, m.State, m.Source)
		}
	}
	for _, item := range s.Settings {
		if item.Key != SettingEnglish && russian(item.Title+item.Detail) {
			t.Errorf("настройка по-русски: %+v", item)
		}
	}
	if russian(s.PlanTitle + strings.Join(s.Plan, " ") + s.OrderNote) {
		t.Errorf("план по-русски: %q %v %q", s.PlanTitle, s.Plan, s.OrderNote)
	}
}
