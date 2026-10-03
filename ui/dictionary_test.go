package ui

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/zemidala/modvault/i18n"
)

// Английский словарь окна должен покрывать всё, что окно показывает само:
// строки скриптов (t("…"), t`…`, plural(…)) и текст страницы. Тест собирает
// их из файлов окна, так что новую строку нельзя забыть.

var (
	cyrillic   = regexp.MustCompile(`[А-Яа-яЁё]`)
	plainCall  = regexp.MustCompile(`(?:^|[^\w.$])t\("((?:[^"\\]|\\.)*)"\)`)
	pluralCall = regexp.MustCompile(`plural\([^,()]+, "([^"]+)", "([^"]+)", "([^"]+)"\)`)
	tagStart   = regexp.MustCompile("(?:^|[^\\w.$])t`")
	pageText   = regexp.MustCompile(`>([^<>]+)<`)
	pageAttr   = regexp.MustCompile(`\b(?:title|placeholder|aria-label)="([^"]+)"`)
	noScripts  = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>`)
	slot       = regexp.MustCompile(`\{\d*\}`)

	blockComment = regexp.MustCompile(`/\*.*?\*/`)
)

// templateKey разбирает шаблон, начинающийся сразу за открывающей кавычкой
// в src[at:], и возвращает его неизменные части через «{}».
func templateKey(src string, at int) (string, int) {
	var parts []string
	var cur strings.Builder
	for i := at; i < len(src); {
		switch {
		case src[i] == '\\':
			switch src[i+1] {
			case 'n':
				cur.WriteByte('\n')
			default:
				cur.WriteByte(src[i+1])
			}
			i += 2
		case src[i] == '`':
			parts = append(parts, cur.String())
			return strings.Join(parts, "{}"), i + 1
		case src[i] == '$' && src[i+1] == '{':
			parts = append(parts, cur.String())
			cur.Reset()
			depth := 0
			for i += 2; ; i++ {
				switch src[i] {
				case '`': // вложенный шаблон: пропускаем целиком
					_, end := templateKey(src, i+1)
					i = end - 1
				case '"', '\'':
					for q := src[i]; ; {
						i++
						if src[i] == '\\' {
							i++
						} else if src[i] == q {
							break
						}
					}
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth < 0 {
					i++
					break
				}
			}
		default:
			cur.WriteByte(src[i])
			i++
		}
	}
	return "", len(src)
}

// windowKeys возвращает строки окна, которым нужен перевод: ключ → где.
func windowKeys(t *testing.T) map[string]string {
	t.Helper()
	page, names, texts := pageScripts(t)
	keys := map[string]string{}
	for _, name := range names {
		src := texts[name]
		if name == "js/en.js" || name == "js/i18n.js" {
			continue
		}
		for _, m := range plainCall.FindAllStringSubmatch(src, -1) {
			s, err := strconv.Unquote(`"` + m[1] + `"`)
			if err != nil {
				t.Errorf("%s: строка %q не разобрана: %v", name, m[1], err)
				continue
			}
			keys[s] = name
		}
		for _, m := range pluralCall.FindAllStringSubmatch(src, -1) {
			keys[m[1]+"|"+m[2]+"|"+m[3]] = name
		}
		for _, loc := range tagStart.FindAllStringIndex(src, -1) {
			key, _ := templateKey(src, loc[1])
			keys[key] = name
		}
		// Русская строка мимо t() осталась бы русской в английском окне.
		for i, line := range strings.Split(src, "\n") {
			code := line
			if at := strings.Index(code, "//"); at >= 0 {
				code = code[:at]
			}
			code = blockComment.ReplaceAllString(code, "")
			rest := plainCall.ReplaceAllString(code, "")
			rest = pluralCall.ReplaceAllString(rest, "")
			for _, loc := range tagStart.FindAllStringIndex(rest, -1) {
				if _, end := templateKey(rest, loc[1]); end <= len(rest) {
					rest = rest[:loc[1]] + strings.Repeat(" ", end-loc[1]) + rest[end:]
				}
			}
			if cyrillic.MatchString(rest) {
				t.Errorf("%s:%d: русская строка не обёрнута в t(): %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
	body := noScripts.ReplaceAllString(page, "")
	for _, m := range pageText.FindAllStringSubmatch(body, -1) {
		if text := strings.TrimSpace(m[1]); cyrillic.MatchString(text) {
			keys[text] = "index.html"
		}
	}
	for _, m := range pageAttr.FindAllStringSubmatch(body, -1) {
		if text := strings.TrimSpace(m[1]); cyrillic.MatchString(text) {
			keys[text] = "index.html"
		}
	}
	return keys
}

func TestWindowDictionary(t *testing.T) {
	data, err := fs.ReadFile(frontend, "frontend/js/en.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	var dict map[string]string
	start := strings.Index(text, "window.MODVAULT_EN = ") + len("window.MODVAULT_EN = ")
	if err := json.Unmarshal([]byte(text[start:strings.LastIndex(text, "}")+1]), &dict); err != nil {
		t.Fatalf("словарь en.js не разобран: %v", err)
	}
	keys := windowKeys(t)
	if len(keys) < 150 {
		t.Fatalf("из окна собрано подозрительно мало строк: %d", len(keys))
	}
	var missing []string
	for key, where := range keys {
		tr, ok := dict[key]
		if !ok {
			missing = append(missing, where+": "+strconv.Quote(key))
			continue
		}
		if strings.Count(key, "|") == 2 {
			if strings.Count(tr, "|") != 1 {
				t.Errorf("%s: у форм %q перевод %q, а нужен вида «one|other»", where, key, tr)
			}
			continue
		}
		if strings.Count(key, "{}") != len(slot.FindAllString(tr, -1)) {
			t.Errorf("%s: у перевода другое число подстановок\n  %q\n  %q", where, key, tr)
		}
		if strings.HasPrefix(key, " ") != strings.HasPrefix(tr, " ") || strings.HasSuffix(key, " ") != strings.HasSuffix(tr, " ") ||
			strings.HasSuffix(key, "\n") != strings.HasSuffix(tr, "\n") {
			t.Errorf("%s: у перевода другие пробелы или переводы строк по краям\n  %q\n  %q", where, key, tr)
		}
		if cyrillic.MatchString(tr) {
			t.Errorf("%s: в переводе остались русские буквы: %q", where, tr)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("без перевода строк окна: %d\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	for key := range dict {
		if _, ok := keys[key]; !ok {
			t.Errorf("в словаре окна есть строка, которой в окне нет: %q", key)
		}
	}
}

// Страница узнаёт язык из js/lang.js: программа подставляет в него язык
// этого запуска.
func TestLanguageScript(t *testing.T) {
	defer i18n.Use(i18n.Russian)
	read := func() string {
		data, err := fs.ReadFile(Assets(), "js/lang.js")
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := read(); !strings.Contains(got, `MODVAULT_LANG = "ru"`) {
		t.Errorf("по-русски: %q", got)
	}
	i18n.Use(i18n.English)
	if got := read(); !strings.Contains(got, `MODVAULT_LANG = "en"`) {
		t.Errorf("по-английски: %q", got)
	}
	// Файл открывается и обычным способом, а остальные файлы — как были.
	f, err := Assets().Open("js/lang.js")
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := f.Stat(); info.Size() == 0 || info.Name() != "lang.js" {
		t.Errorf("сведения о файле: %+v", info)
	}
	f.Close()
	if _, err := fs.ReadFile(Assets(), "index.html"); err != nil {
		t.Error(err)
	}
}
