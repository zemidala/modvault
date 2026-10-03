// Package rules — правила порядка и зависимостей модов: сортировка без
// потерь и проверки. Моды здесь — имена папок, по которым их грузит игра.
package rules

import (
	"container/heap"
	"sort"
	"strings"
)

// Kind — вид правила.
type Kind string

const (
	After    Kind = "after"    // Mod грузится после Other
	Before   Kind = "before"   // Mod грузится до Other
	Requires Kind = "requires" // Mod не работает без Other
)

// Rule — правило про мод Mod и мод Other.
type Rule struct {
	Kind   Kind
	Mod    string
	Other  string
	Source string // откуда правило: «файл .mod», «загрузчик»
}

func key(s string) string { return strings.ToLower(s) }

// Result — итог сортировки.
type Result struct {
	Order []string
	// Cycles — группы модов, правила которых противоречат друг другу. Моды
	// из них не теряются: между собой они остаются в прежнем порядке.
	Cycles [][]string
}

// Sort упорядочивает моды по правилам «до/после», меняя исходный порядок
// как можно меньше: из модов, которым ничто не мешает встать, первым идёт
// тот, что раньше стоял выше. Правила про моды не из списка не действуют.
func Sort(order []string, rules []Rule) Result {
	index := make(map[string]int, len(order))
	for i, m := range order {
		if _, dup := index[key(m)]; !dup {
			index[key(m)] = i
		}
	}

	// Рёбра «раньше → позже» по позициям в исходном порядке.
	succ := make([][]int, len(order))
	indeg := make([]int, len(order))
	seen := map[[2]int]bool{}
	addEdge := func(from, to int) {
		if from == to || seen[[2]int{from, to}] {
			return
		}
		seen[[2]int{from, to}] = true
		succ[from] = append(succ[from], to)
		indeg[to]++
	}
	for _, r := range rules {
		m, ok1 := index[key(r.Mod)]
		o, ok2 := index[key(r.Other)]
		if !ok1 || !ok2 {
			continue
		}
		switch r.Kind {
		case After:
			addEdge(o, m)
		case Before:
			addEdge(m, o)
		}
	}

	var res Result
	placed := make([]bool, len(order))
	ready := &intHeap{}
	for i := range order {
		if indeg[i] == 0 {
			heap.Push(ready, i)
		}
	}
	for len(res.Order) < len(order) {
		if ready.Len() == 0 {
			// Правила замкнулись в круг: ставим самый верхний из оставшихся
			// модов вопреки правилам — так никто не пропадёт.
			cycle := remainingCycles(order, succ, placed)
			res.Cycles = append(res.Cycles, cycle...)
			for i := range order {
				if !placed[i] {
					indeg[i] = 0
					heap.Push(ready, i)
					break
				}
			}
		}
		i := heap.Pop(ready).(int)
		if placed[i] {
			continue
		}
		placed[i] = true
		res.Order = append(res.Order, order[i])
		for _, j := range succ[i] {
			if placed[j] {
				continue
			}
			indeg[j]--
			if indeg[j] == 0 {
				heap.Push(ready, j)
			}
		}
	}
	res.Cycles = dedupCycles(res.Cycles)
	return res
}

// remainingCycles находит среди неразмещённых модов группы, связанные
// правилами в круг (сильно связные компоненты больше одного мода).
func remainingCycles(order []string, succ [][]int, placed []bool) [][]string {
	n := len(order)
	idx := make([]int, n)
	low := make([]int, n)
	on := make([]bool, n)
	for i := range idx {
		idx[i] = -1
	}
	var stack []int
	var out [][]string
	counter := 0
	var visit func(v int)
	visit = func(v int) {
		idx[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		on[v] = true
		for _, w := range succ[v] {
			if placed[w] {
				continue
			}
			if idx[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], idx[w])
			}
		}
		if low[v] == idx[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			if len(comp) > 1 {
				sort.Ints(comp)
				names := make([]string, len(comp))
				for i, c := range comp {
					names[i] = order[c]
				}
				out = append(out, names)
			}
		}
	}
	for v := 0; v < n; v++ {
		if !placed[v] && idx[v] < 0 {
			visit(v)
		}
	}
	return out
}

func dedupCycles(cycles [][]string) [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, c := range cycles {
		k := key(strings.Join(c, "\x00"))
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// Violations перечисляет правила «до/после», которые нарушает порядок.
func Violations(order []string, rules []Rule) []Rule {
	pos := make(map[string]int, len(order))
	for i, m := range order {
		if _, dup := pos[key(m)]; !dup {
			pos[key(m)] = i
		}
	}
	var out []Rule
	for _, r := range rules {
		m, ok1 := pos[key(r.Mod)]
		o, ok2 := pos[key(r.Other)]
		if !ok1 || !ok2 || m == o {
			continue
		}
		if (r.Kind == After && m < o) || (r.Kind == Before && m > o) {
			out = append(out, r)
		}
	}
	return out
}

// Missing перечисляет правила «требует», для которых нужного мода нет среди
// загружаемых.
func Missing(loaded []string, rules []Rule) []Rule {
	present := make(map[string]bool, len(loaded))
	for _, m := range loaded {
		present[key(m)] = true
	}
	var out []Rule
	for _, r := range rules {
		if r.Kind == Requires && present[key(r.Mod)] && !present[key(r.Other)] {
			out = append(out, r)
		}
	}
	return out
}

type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
