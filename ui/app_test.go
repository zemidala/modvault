package ui

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Всё, на что ссылаются страница и стили, должно быть вшито в программу:
// окно работает без интернета.
func TestAssets(t *testing.T) {
	assets := Assets()
	ref := regexp.MustCompile(`(?:href|src)="([^"#]+)"|url\("?([^")]+)"?\)`)

	for _, name := range []string{"index.html", "app.css"} {
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(data), -1) {
			target := m[1] + m[2]
			if strings.HasPrefix(target, "#") {
				continue // ссылка внутри самой страницы
			}
			if strings.Contains(target, "://") {
				t.Errorf("%s ссылается на внешний адрес %s", name, target)
				continue
			}
			if _, err := fs.Stat(assets, target); err != nil {
				t.Errorf("%s ссылается на %s, которого нет: %v", name, target, err)
			}
		}
	}
}

func TestWindowState(t *testing.T) {
	t.Setenv("LocalAppData", t.TempDir())

	// Первый запуск: размер по умолчанию, место выбирает система.
	if ws := LoadWindow(); ws.Width != defaultWidth || ws.Height != defaultHeight || ws.Placed || ws.Maximised {
		t.Errorf("по умолчанию: %+v", ws)
	}

	// Обычное окно запоминается целиком.
	normal := nextWindow(LoadWindow(), 1500, 1000, 40, 60, false, false)
	if err := saveWindow(normal); err != nil {
		t.Fatal(err)
	}
	want := WindowState{Width: 1500, Height: 1000, X: 40, Y: 60, Placed: true}
	if got := LoadWindow(); got != want {
		t.Errorf("после сохранения: %+v", got)
	}

	// Развёрнутое на весь экран: запоминается разворот, а размер остаётся
	// прежним — к нему окно вернётся, когда разворот снимут.
	max := nextWindow(normal, 2560, 1400, 0, 0, true, false)
	if !max.Maximised || max.Width != 1500 || max.Height != 1000 || max.X != 40 {
		t.Errorf("развёрнутое: %+v", max)
	}
	// Закрыли свёрнутым: ничего не меняется.
	if got := nextWindow(max, 160, 28, -32000, -32000, false, true); got != max {
		t.Errorf("свёрнутое: %+v", got)
	}
	// Разворот сняли и закрыли: снова обычное окно.
	if got := nextWindow(max, 1300, 900, 10, 10, false, false); got.Maximised || got.Width != 1300 {
		t.Errorf("после снятия разворота: %+v", got)
	}

	// Испорченный файл и негодный размер не мешают открыть окно.
	os.WriteFile(windowPath(), []byte("{"), 0o644)
	if ws := LoadWindow(); ws.Width != defaultWidth {
		t.Errorf("испорченный файл: %+v", ws)
	}
	saveWindow(WindowState{Width: 10, Height: 10})
	if ws := LoadWindow(); ws.Width != defaultWidth || ws.Height != defaultHeight {
		t.Errorf("негодный размер: %+v", ws)
	}
}

func TestOnScreen(t *testing.T) {
	if !onScreen(100, 100) {
		t.Error("точка в углу основного экрана не найдена ни на одном экране")
	}
	if onScreen(-200000, -200000) {
		t.Error("точка далеко за экранами принята за видимую")
	}
}
