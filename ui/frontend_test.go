package ui

import (
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Страница окна — простые файлы без сборки, и ошибку в них некому поймать
// до запуска. Эти проверки ловят самое частое: скрипт, которого нет;
// элемент, которого нет на странице; вызов программы, которого она не знает.

var (
	scriptSrc   = regexp.MustCompile(`<script src="([^"]+)"`)
	elementID   = regexp.MustCompile(`\bid="([^"]+)"`)
	usedID      = regexp.MustCompile(`\$\("([^"]+)"\)`)
	backendCall = regexp.MustCompile(`backend\(\)\.(\w+)\(`)
	definition  = regexp.MustCompile(`(?m)^(?:async )?function (\w+)\(|^(?:const|let) (\w+)\b`)
)

// pageScripts возвращает скрипты страницы в порядке загрузки: имя → текст.
func pageScripts(t *testing.T) (string, []string, map[string]string) {
	t.Helper()
	page, err := fs.ReadFile(frontend, "frontend/index.html")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	texts := map[string]string{}
	for _, m := range scriptSrc.FindAllStringSubmatch(string(page), -1) {
		data, err := fs.ReadFile(frontend, "frontend/"+m[1])
		if err != nil {
			t.Fatalf("страница подключает %s, а такого файла нет", m[1])
		}
		names = append(names, m[1])
		texts[m[1]] = string(data)
	}
	if len(names) == 0 {
		t.Fatal("страница не подключает ни одного скрипта")
	}
	return string(page), names, texts
}

func TestFrontendScripts(t *testing.T) {
	_, names, _ := pageScripts(t)
	listed := map[string]bool{}
	for _, name := range names {
		listed[name] = true
	}
	// Скрипт, лежащий в папке, но не подключённый, — забытый или лишний.
	entries, err := fs.ReadDir(frontend, "frontend/js")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name := "js/" + e.Name(); strings.HasSuffix(name, ".js") && !listed[name] {
			t.Errorf("%s лежит в папке, но страница его не подключает", name)
		}
	}
}

func TestFrontendElements(t *testing.T) {
	page, names, texts := pageScripts(t)
	ids := map[string]bool{}
	for _, m := range elementID.FindAllStringSubmatch(page, -1) {
		if ids[m[1]] {
			t.Errorf("на странице два элемента с id=%q", m[1])
		}
		ids[m[1]] = true
	}
	for _, name := range names {
		for _, m := range usedID.FindAllStringSubmatch(texts[name], -1) {
			if !ids[m[1]] {
				t.Errorf("%s обращается к элементу %q, а его нет на странице", name, m[1])
			}
		}
	}
}

func TestFrontendBackendCalls(t *testing.T) {
	_, names, texts := pageScripts(t)
	app := reflect.TypeOf(&App{})
	for _, name := range names {
		for _, m := range backendCall.FindAllStringSubmatch(texts[name], -1) {
			if _, ok := app.MethodByName(m[1]); !ok {
				t.Errorf("%s вызывает %s, а программа такого не умеет", name, m[1])
			}
		}
	}
}

// Скрипты делят одну область имён: одно имя, объявленное в двух файлах,
// остановило бы загрузку второго.
func TestFrontendNames(t *testing.T) {
	_, names, texts := pageScripts(t)
	where := map[string]string{}
	for _, name := range names {
		for _, m := range definition.FindAllStringSubmatch(texts[name], -1) {
			id := m[1] + m[2]
			if prev, ok := where[id]; ok {
				t.Errorf("%s объявлено дважды: в %s и в %s", id, prev, name)
			}
			where[id] = name
		}
	}
}
