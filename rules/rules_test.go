package rules

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func after(mod, other string) Rule  { return Rule{Kind: After, Mod: mod, Other: other} }
func before(mod, other string) Rule { return Rule{Kind: Before, Mod: mod, Other: other} }

func TestSort(t *testing.T) {
	tests := []struct {
		name  string
		order string
		rules []Rule
		want  string
	}{
		{"без правил порядок прежний", "c a b", nil, "c a b"},
		{"после", "a b c", []Rule{after("a", "c")}, "b c a"},
		{"до", "a b c", []Rule{before("c", "a")}, "b c a"},
		{"уже соблюдено — ничего не двигается", "a b c d", []Rule{after("d", "a"), before("a", "c")}, "a b c d"},
		{"цепочка", "d c b a", []Rule{after("d", "c"), after("c", "b"), after("b", "a")}, "a b c d"},
		{"регистр не важен", "Flux afk", []Rule{after("flux", "AFK")}, "afk Flux"},
		{"правило про отсутствующий мод не действует", "a b", []Rule{after("a", "нет"), before("нет", "b")}, "a b"},
		{"незатронутые моды остаются на местах", "x a y b z", []Rule{after("a", "b")}, "x y b a z"},
		{"правило о себе не действует", "a b", []Rule{after("a", "a")}, "a b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Sort(strings.Fields(tt.order), tt.rules)
			if got := strings.Join(res.Order, " "); got != tt.want {
				t.Errorf("порядок = %q, want %q", got, tt.want)
			}
			if len(res.Cycles) != 0 {
				t.Errorf("циклы: %v", res.Cycles)
			}
			if v := Violations(res.Order, tt.rules); len(v) != 0 {
				t.Errorf("результат нарушает правила: %+v", v)
			}
		})
	}
}

// Противоречивые правила: цикл показывается, моды не теряются.
func TestSortCycle(t *testing.T) {
	order := strings.Fields("x a b c y d")
	rs := []Rule{after("a", "b"), after("b", "c"), after("c", "a"), after("d", "x")}
	res := Sort(order, rs)

	if len(res.Order) != len(order) {
		t.Fatalf("моды потеряны: %v", res.Order)
	}
	got := append([]string(nil), res.Order...)
	sort.Strings(got)
	want := append([]string(nil), order...)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("набор модов изменился: %v", res.Order)
	}
	if len(res.Cycles) != 1 || strings.Join(res.Cycles[0], " ") != "a b c" {
		t.Errorf("циклы = %v, want [[a b c]]", res.Cycles)
	}
	// Правило вне цикла соблюдено.
	if v := Violations(res.Order, []Rule{after("d", "x")}); len(v) != 0 {
		t.Errorf("правило вне цикла нарушено: %v", res.Order)
	}
}

func TestViolationsAndMissing(t *testing.T) {
	order := strings.Fields("a b c")
	rs := []Rule{
		after("a", "c"), before("c", "b"), after("c", "a"),
		{Kind: Requires, Mod: "a", Other: "dmf_ext"},
		{Kind: Requires, Mod: "b", Other: "A"},
		{Kind: Requires, Mod: "нет", Other: "тоже нет"},
	}
	if v := Violations(order, rs); len(v) != 2 || v[0].Mod != "a" || v[1].Mod != "c" {
		t.Errorf("нарушения = %+v", v)
	}
	missing := Missing(order, rs)
	if len(missing) != 1 || missing[0].Mod != "a" || missing[0].Other != "dmf_ext" {
		t.Errorf("недостающие = %+v", missing)
	}
}

// Большой набор со случайными правилами: никто не теряется, повторная
// сортировка ничего не меняет, а без циклов все правила соблюдены.
func TestSortLargeStable(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 200; round++ {
		n := 100 + rng.Intn(60)
		order := make([]string, n)
		for i := range order {
			order[i] = fmt.Sprintf("mod%03d", i)
		}
		rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })

		var rs []Rule
		for i := 0; i < rng.Intn(120); i++ {
			a, b := order[rng.Intn(n)], order[rng.Intn(n)]
			if rng.Intn(2) == 0 {
				rs = append(rs, after(a, b))
			} else {
				rs = append(rs, before(a, b))
			}
		}

		res := Sort(order, rs)
		if len(res.Order) != n {
			t.Fatalf("раунд %d: было %d модов, стало %d", round, n, len(res.Order))
		}
		seen := map[string]bool{}
		for _, m := range res.Order {
			if seen[m] {
				t.Fatalf("раунд %d: мод %s дважды", round, m)
			}
			seen[m] = true
		}
		if len(res.Cycles) == 0 {
			if v := Violations(res.Order, rs); len(v) != 0 {
				t.Fatalf("раунд %d: правила без циклов нарушены: %+v", round, v[0])
			}
		}
		again := Sort(res.Order, rs)
		if strings.Join(again.Order, " ") != strings.Join(res.Order, " ") {
			t.Fatalf("раунд %d: повторная сортировка изменила порядок", round)
		}
	}
}
