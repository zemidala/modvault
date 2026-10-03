package manager

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zemidala/modvault/core/store"
)

// benchApp — хранилище со 140 модами по 20 файлов, всё развёрнуто.
func benchApp(b *testing.B) (*Manager, []string) {
	a, _, g := newGame(b)
	if _, err := a.setGame(g); err != nil {
		b.Fatal(err)
	}
	a.addArchive(writeZip(b, "dml.zip", dmlArchive))
	body := strings.Repeat("-- lua\n", 3000) // около 20 КБ
	var ids []string
	for i := 0; i < 140; i++ {
		name := fmt.Sprintf("Mod%03d", i)
		files := map[string]string{name + "/" + name + ".mod": "return {}"}
		for f := 0; f < 19; f++ {
			files[fmt.Sprintf("%s/scripts/mods/%s/file%02d.lua", name, name, f)] = body
		}
		// Прямо в хранилище: состояние окна после каждого мода здесь не нужно.
		v, err := a.store.Add(writeZip(b, name+".zip", files), store.Info{Name: name})
		if err != nil {
			b.Fatal(err)
		}
		ids = append(ids, v.ModID)
	}
	if _, err := a.SetEnabledMany(ids, true); err != nil {
		b.Fatal(err)
	}
	if _, err := a.Deploy(); err != nil {
		b.Fatal(err)
	}
	return a, ids
}

// BenchmarkState — сколько стоит пересчитать состояние окна на большом
// наборе: 140 модов по 20 файлов, всё развёрнуто. Столько работы стоит
// за каждым щелчком в окне.
func BenchmarkState(b *testing.B) {
	a, _ := benchApp(b)
	time.Sleep(3 * time.Second) // только что записанные файлы в память не попадают
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.State(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSwitch — сколько стоит убрать из игры и положить обратно 140
// модов по 20 файлов: так переключаются наборы и идёт поиск сбойного мода.
func BenchmarkSwitch(b *testing.B) {
	a, ids := benchApp(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, enabled := range []bool{false, true} {
			if _, err := a.SetEnabledMany(ids, enabled); err != nil {
				b.Fatal(err)
			}
			if _, err := a.Deploy(); err != nil {
				b.Fatal(err)
			}
		}
	}
}
