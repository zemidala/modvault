package journal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type plan struct {
	Steps []string `json:"steps"`
}

func TestLifecycle(t *testing.T) {
	dir := t.TempDir()
	if p, err := Load(dir); err != nil || p != nil {
		t.Fatalf("пустая папка: %+v, %v", p, err)
	}

	j, err := Begin(dir, "op-1", plan{Steps: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(dir, "op-2", plan{}); !errors.Is(err, ErrPending) {
		t.Errorf("вторая операция: %v, want ErrPending", err)
	}
	if err := j.Done(); err != nil {
		t.Fatal(err)
	}
	if err := j.Done(); err != nil {
		t.Fatal(err)
	}
	j.Close()

	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "op-1" || p.Done != 2 {
		t.Errorf("Load = %s, %d", p.ID, p.Done)
	}
	var got plan
	if err := json.Unmarshal(p.Data, &got); err != nil || len(got.Steps) != 3 {
		t.Errorf("план = %+v, %v", got, err)
	}

	if err := Clear(dir); err != nil {
		t.Fatal(err)
	}
	if p, _ := Load(dir); p != nil {
		t.Error("журнал остался после Clear")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("в папке осталось %d файлов", len(entries))
	}
}

func TestFinish(t *testing.T) {
	dir := t.TempDir()
	j, err := Begin(dir, "op", plan{})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Finish(); err != nil {
		t.Fatal(err)
	}
	next, err := Begin(dir, "next", plan{})
	if err != nil {
		t.Fatalf("после Finish новая операция не начинается: %v", err)
	}
	next.Close()
}

// Оборванная последняя отметка и отметки не по порядку не считаются.
func TestTornLog(t *testing.T) {
	dir := t.TempDir()
	j, err := Begin(dir, "op", plan{})
	if err != nil {
		t.Fatal(err)
	}
	j.Close()

	for log, want := range map[string]int{
		"":               0,
		"0\n1\n":         2,
		"0\n1\n2":        3,
		"0\n1\nмусор\n":  2,
		"0\n2\n3\n":      1,
		"0\n1\n\x00\x00": 2,
	} {
		os.WriteFile(filepath.Join(dir, logFile), []byte(log), 0o644)
		p, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if p.Done != want {
			t.Errorf("журнал %q: Done = %d, want %d", log, p.Done, want)
		}
	}
}

// Отметки без плана — след оборванного Clear — не мешают новой операции.
func TestStaleLog(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, logFile), []byte("0\n1\n2\n"), 0o644)
	if p, err := Load(dir); err != nil || p != nil {
		t.Fatalf("отметки без плана: %+v, %v", p, err)
	}
	j, err := Begin(dir, "op", plan{})
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
	if p, _ := Load(dir); p.Done != 0 {
		t.Errorf("старые отметки перешли в новую операцию: %d", p.Done)
	}
}

func TestCorruptPlan(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, planFile), []byte("{обрезан"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Error("повреждённый журнал прочитан без ошибки")
	}
}
