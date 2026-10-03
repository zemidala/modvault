package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Словарь должен покрывать всё, что программа показывает: строка, которой
// в нём нет, осталась бы русской посреди английского интерфейса. Тест
// собирает строки прямо из кода, так что новую строку нельзя забыть.

func constString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return constString(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := constString(x.X)
		b, ok2 := constString(x.Y)
		return a + b, ok1 && ok2
	}
	return "", false
}

// sourceKeys возвращает строки, которые код отдаёт на перевод: ключ → где.
func sourceKeys(t *testing.T) map[string]string {
	t.Helper()
	keys := map[string]string{}
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "bin" || name == "build" || name == "frontend" || (strings.HasPrefix(name, ".") && name != "..") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(filepath.Dir(path)) == "i18n" {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch f := call.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				if x, ok := f.X.(*ast.Ident); ok {
					name = x.Name + "." + f.Sel.Name
				}
			}
			where := filepath.ToSlash(path) + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
			arg := -1
			switch name {
			case "i18n.T", "i18n.Sprintf", "i18n.Errorf", "i18n.NewError":
				arg = 0
			case "i18n.Fprintf":
				arg = 1
			case "plural", "Plural", "manager.Plural", "i18n.Plural":
				if len(call.Args) == 4 {
					one, ok1 := constString(call.Args[1])
					few, ok2 := constString(call.Args[2])
					many, ok3 := constString(call.Args[3])
					if ok1 && ok2 && ok3 {
						keys[one+"|"+few+"|"+many] = where
					}
				}
			}
			if arg >= 0 && arg < len(call.Args) {
				if s, ok := constString(call.Args[arg]); ok {
					keys[s] = where
				} else {
					t.Errorf("%s: %s получает строку, составленную на ходу — перевести её нельзя", where, name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

var verb = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

// verbs — глаголы формата по номерам аргументов, чтобы перевод мог менять
// их порядок (%[2]s), но не набор.
func verbs(format string) string {
	var out []string
	next := 1
	for _, m := range verb.FindAllStringSubmatch(format, -1) {
		v := m[0]
		if v == "%%" {
			continue
		}
		n := next
		if m[1] != "" {
			n, _ = strconv.Atoi(strings.Trim(m[1], "[]"))
		}
		next = n + 1
		out = append(out, strconv.Itoa(n)+string(v[len(v)-1]))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestEnglishCatalog(t *testing.T) {
	keys := sourceKeys(t)
	if len(keys) < 300 {
		t.Fatalf("из кода собрано подозрительно мало строк: %d", len(keys))
	}
	var missing []string
	for key, where := range keys {
		tr, ok := en[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		if strings.Count(key, "|") == 2 && !strings.Contains(key, "%") && !strings.Contains(key, " | ") {
			// Формы слова: в переводе их две — «one|other».
			if strings.Count(tr, "|") != 1 {
				t.Errorf("%s: у форм %q перевод %q, а нужен вида «one|other»", where, key, tr)
			}
			continue
		}
		if verbs(key) != verbs(tr) {
			t.Errorf("%s: у перевода другие подстановки\n  %q\n  %q", where, key, tr)
		}
		if strings.HasPrefix(key, " ") != strings.HasPrefix(tr, " ") || strings.HasSuffix(key, " ") != strings.HasSuffix(tr, " ") ||
			strings.HasSuffix(key, "\n") != strings.HasSuffix(tr, "\n") || strings.HasPrefix(key, "\n") != strings.HasPrefix(tr, "\n") {
			t.Errorf("%s: у перевода другие пробелы или переводы строк по краям\n  %q\n  %q", where, key, tr)
		}
	}
	sort.Strings(missing)
	// Список непереведённого можно выгрузить в файл: MODVAULT_I18N_MISSING=путь.
	if out := os.Getenv("MODVAULT_I18N_MISSING"); out != "" {
		var b strings.Builder
		for _, key := range missing {
			b.WriteString(strconv.Quote(key) + "\n")
		}
		os.WriteFile(out, []byte(b.String()), 0o644)
	}
	if len(missing) > 0 {
		show := missing
		if len(show) > 10 {
			show = show[:10]
		}
		t.Errorf("без перевода строк: %d, например:\n  %s", len(missing), strings.Join(show, "\n  "))
	}
	// Лишние записи — следы убранных строк.
	for key := range en {
		if _, ok := keys[key]; !ok {
			t.Errorf("в словаре есть строка, которой нет в коде: %q", key)
		}
	}
}

func TestTranslate(t *testing.T) {
	defer Use(Russian)
	saved := en
	defer func() { en = saved }()
	en = map[string]string{
		"«%s» в наборе «%s»": "set “%[2]s” has “%[1]s”",
		"мод|мода|модов":     "mod|mods",
		"файл занят":         "file is busy",
	}
	busy := NewError("файл занят") // создана до выбора языка

	if got := Sprintf("«%s» в наборе «%s»", "A", "B"); got != "«A» в наборе «B»" {
		t.Errorf("по-русски: %q", got)
	}
	if Plural(1, "мод", "мода", "модов") != "мод" || Plural(3, "мод", "мода", "модов") != "мода" || Plural(11, "мод", "мода", "модов") != "модов" {
		t.Error("русские формы слова")
	}

	Use("en-US")
	if Language() != English {
		t.Fatalf("язык = %q", Language())
	}
	if got := Sprintf("«%s» в наборе «%s»", "A", "B"); got != "set “B” has “A”" {
		t.Errorf("по-английски: %q", got)
	}
	if Plural(1, "мод", "мода", "модов") != "mod" || Plural(5, "мод", "мода", "модов") != "mods" {
		t.Error("английские формы слова")
	}
	if busy.Error() != "file is busy" || T("нет в словаре") != "нет в словаре" {
		t.Errorf("ошибка: %q, строка без перевода: %q", busy.Error(), T("нет в словаре"))
	}
	if err := Errorf("обёртка: %w", busy); err.Error() != "обёртка: file is busy" {
		t.Errorf("обёрнутая ошибка: %q", err)
	}
}

// Пока язык не выбран, интерфейс английский; выбор русского запоминается.
func TestDefaultLanguage(t *testing.T) {
	UseFile(filepath.Join(t.TempDir(), "language"))
	defer UseFile("")
	if Saved() != English {
		t.Errorf("без выбора язык %q", Saved())
	}
	if err := Save(Russian); err != nil {
		t.Fatal(err)
	}
	if Saved() != Russian {
		t.Errorf("после выбора русского язык %q", Saved())
	}
}
