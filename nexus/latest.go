package nexus

import (
	"strconv"
	"strings"
)

// Latest находит файл, до которого стоит обновиться. Если известен
// установленный файл (have), идёт по истории замен от него; иначе берёт
// самый свежий файл из основных. ok ложно, если подходящего файла нет.
func (f Files) Latest(have int) (File, bool) {
	byID := make(map[int]File, len(f.Files))
	for _, file := range f.Files {
		byID[file.ID] = file
	}
	if have != 0 {
		next := make(map[int]int, len(f.Updates))
		for _, u := range f.Updates {
			next[u.Old] = u.New
		}
		cur := have
		for range f.Updates { // не больше шагов, чем замен: история с петлёй не зациклит
			n, ok := next[cur]
			if !ok {
				break
			}
			cur = n
		}
		if file, ok := byID[cur]; ok && cur != have && usable(file) {
			return file, true
		}
		if file, ok := byID[have]; ok && usable(file) {
			return file, true // установленный файл на месте, и его никто не заменил
		}
	}
	var best File
	found := false
	for _, file := range f.Files {
		if file.Category != CategoryMain {
			continue
		}
		if !found || file.Uploaded > best.Uploaded {
			best, found = file, true
		}
	}
	return best, found
}

// Replaces сообщает, что файл newID пришёл на смену файлу oldID — сразу
// или через несколько замен.
func (f Files) Replaces(oldID, newID int) bool {
	next := make(map[int]int, len(f.Updates))
	for _, u := range f.Updates {
		next[u.Old] = u.New
	}
	cur := oldID
	for range f.Updates {
		n, ok := next[cur]
		if !ok {
			return false
		}
		if n == newID {
			return true
		}
		cur = n
	}
	return false
}

// Find возвращает файл по номеру.
func (f Files) Find(id int) (File, bool) {
	for _, file := range f.Files {
		if file.ID == id {
			return file, true
		}
	}
	return File{}, false
}

func usable(f File) bool {
	return f.Category != CategoryOld && f.Category != CategoryArchived && f.Category != ""
}

// Newer сообщает, что версия latest новее установленной. Версии из чисел
// сравниваются по числам; остальные считаются новее, если просто отличаются.
func Newer(installed, latest string) bool {
	a, b := normVersion(installed), normVersion(latest)
	if b == "" || a == b {
		return false
	}
	if a == "" {
		return true
	}
	na, oka := numbers(a)
	nb, okb := numbers(b)
	if !oka || !okb {
		return true
	}
	for i := 0; i < len(na) || i < len(nb); i++ {
		var x, y int
		if i < len(na) {
			x = na[i]
		}
		if i < len(nb) {
			y = nb[i]
		}
		if x != y {
			return y > x
		}
	}
	return false
}

func normVersion(v string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
}

func numbers(v string) ([]int, bool) {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
	if len(parts) == 0 {
		return nil, false
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}
