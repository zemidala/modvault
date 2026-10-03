package darktide

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/rules"
)

const (
	loaderScript = "mods/base/mod_manager.lua"
	loaderLog    = "mods/auto_mod_loader_log.txt"
)

var (
	luaBlockComment = regexp.MustCompile(`(?s)--\[\[.*?\]\]`)
	luaLineComment  = regexp.MustCompile(`--[^\n]*`)
	luaString       = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
	// load_after = { "a", "b" } в файле .mod
	modRuleField = regexp.MustCompile(`\b(load_before|load_after|require)\s*=\s*\{([^{}]*)\}`)
	// local _audio = { "Audio" } — общий список, на который ссылаются правила загрузчика
	luaLocalList = regexp.MustCompile(`\blocal\s+(\w+)\s*=\s*\{([^{}]*)\}`)
	// local after_presets = { ... }
	presetTable = regexp.MustCompile(`\blocal\s+(before|after|require)_presets\s*=\s*\{`)
	// Мод = { "a" }  |  Мод = _audio  |  ["Мод"] = { ... }
	presetEntry = regexp.MustCompile(`(?:\[\s*["']([^"']+)["']\s*\]|(\w+))\s*=\s*(?:\{([^{}]*)\}|(\w+))`)
	// 12:"Scoreboard ("1.4.0) в журнале загрузчика
	logOrderLine = regexp.MustCompile(`^\d+:"(.+?)(?: \("[^)]*\))?\s*$`)
)

func stripLuaComments(src []byte) string {
	s := luaBlockComment.ReplaceAll(src, nil)
	return string(luaLineComment.ReplaceAll(s, nil))
}

func luaStrings(list string) []string {
	var out []string
	for _, m := range luaString.FindAllStringSubmatch(list, -1) {
		out = append(out, m[1]+m[2])
	}
	return out
}

var ruleKinds = map[string]rules.Kind{
	"load_before": rules.Before, "before": rules.Before,
	"load_after": rules.After, "after": rules.After,
	"require": rules.Requires,
}

// ParseModRules читает из файла .mod правила, которые задал автор мода:
// load_before, load_after и require.
func ParseModRules(folder string, modFile []byte) []rules.Rule {
	var out []rules.Rule
	for _, m := range modRuleField.FindAllStringSubmatch(stripLuaComments(modFile), -1) {
		for _, other := range luaStrings(m[2]) {
			out = append(out, rules.Rule{Kind: ruleKinds[m[1]], Mod: folder, Other: other, Source: "файл " + folder + ".mod"})
		}
	}
	return out
}

// ParseLoaderPresets читает правила, встроенные в загрузчик AML для
// известных модов. По его логике они действуют, только если сам мод не
// задал правило того же вида, — это учитывает Ordering.
func ParseLoaderPresets(script []byte) []rules.Rule {
	src := stripLuaComments(script)
	lists := map[string][]string{}
	for _, m := range luaLocalList.FindAllStringSubmatch(src, -1) {
		lists[m[1]] = luaStrings(m[2])
	}

	var out []rules.Rule
	for _, loc := range presetTable.FindAllStringSubmatchIndex(src, -1) {
		kind := ruleKinds[src[loc[2]:loc[3]]]
		body := braceBody(src[loc[1]:])
		for _, e := range presetEntry.FindAllStringSubmatch(body, -1) {
			mod := e[1] + e[2]
			others := luaStrings(e[3])
			if e[4] != "" {
				others = lists[e[4]]
			}
			for _, other := range others {
				out = append(out, rules.Rule{Kind: kind, Mod: mod, Other: other, Source: "загрузчик модов"})
			}
		}
	}
	return out
}

// braceBody возвращает текст до фигурной скобки, закрывающей уже открытую.
func braceBody(s string) string {
	depth := 1
	for i, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// IsAutoLoader сообщает, что загрузчик сам находит и упорядочивает моды
// и не читает mod_load_order.txt (AML, «Auto Mod Loading and Ordering»).
func IsAutoLoader(script []byte) bool {
	return bytes.Contains(script, []byte("aml_hook_load_order")) || bytes.Contains(script, []byte("AML IGNORES mod_load_order"))
}

// ParseLoaderLog читает из журнала AML порядок, в котором игра загрузила
// моды в последний раз.
func ParseLoaderLog(log []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(log))
	for sc.Scan() {
		if m := logOrderLine.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// Ordering собирает всё, что известно о порядке загрузки: правила из
// файлов .mod включённых модов, правила загрузчика и — для AML — порядок
// из его журнала.
func (*Darktide) Ordering(ctx game.OrderContext) game.Ordering {
	var ord game.Ordering
	own := map[string]map[rules.Kind]bool{} // у каких модов есть свои правила какого вида
	var script []byte

	for _, m := range ctx.Mods {
		if !m.Enabled {
			continue
		}
		for _, dst := range m.Layout.Paths {
			if strings.EqualFold(dst, loaderScript) {
				if data, err := ctx.Read(m.ModID, dst); err == nil {
					script = data
				}
			}
			folder, ok := modFile(dst)
			if !ok || !strings.EqualFold(strings.TrimSuffix(path.Base(dst), path.Ext(dst)), folder) {
				continue
			}
			data, err := ctx.Read(m.ModID, dst)
			if err != nil {
				continue
			}
			for _, r := range ParseModRules(folder, data) {
				ord.Rules = append(ord.Rules, r)
				k := strings.ToLower(folder)
				if own[k] == nil {
					own[k] = map[rules.Kind]bool{}
				}
				own[k][r.Kind] = true
			}
		}
	}

	if script != nil {
		ord.Auto = IsAutoLoader(script)
		for _, r := range ParseLoaderPresets(script) {
			if !own[strings.ToLower(r.Mod)][r.Kind] {
				ord.Rules = append(ord.Rules, r)
			}
		}
	}
	if ord.Auto {
		p := filepath.Join(ctx.Dir, filepath.FromSlash(loaderLog))
		if data, err := os.ReadFile(p); err == nil {
			ord.LastOrder = ParseLoaderLog(data)
			if info, err := os.Stat(p); err == nil {
				ord.LastOrderTime = info.ModTime()
			}
		}
	}

	// Загрузчик и фреймворк грузятся первыми сами: правила про них ни на что
	// не влияют, а в списке модов их нет.
	kept := ord.Rules[:0]
	for _, r := range ord.Rules {
		o := strings.ToLower(r.Other)
		if o != loaderFolder && o != frameworkFolder {
			kept = append(kept, r)
		}
	}
	ord.Rules = kept
	return ord
}
