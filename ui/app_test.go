package ui

import (
	"io/fs"
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
