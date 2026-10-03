package fsx

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")

	if err := WriteFile(path, []byte("первый")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "первый" {
		t.Fatalf("содержимое = %q", got)
	}

	if err := WriteFile(path, []byte("второй")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "второй" {
		t.Fatalf("после замены содержимое = %q", got)
	}

	if names := dirNames(t, dir); len(names) != 1 {
		t.Errorf("в папке остались лишние файлы: %v", names)
	}
}

func TestWriteFileNoDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "нет", "a.txt")
	if err := WriteFile(path, []byte("x")); err == nil {
		t.Fatal("запись в несуществующую папку прошла без ошибки")
	}
}

func TestWriterAbort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := WriteFile(path, []byte("старое")); err != nil {
		t.Fatal(err)
	}

	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("новое, но недописанное")); err != nil {
		t.Fatal(err)
	}
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, path); got != "старое" {
		t.Errorf("после Abort содержимое = %q", got)
	}
	if names := dirNames(t, dir); len(names) != 1 {
		t.Errorf("после Abort остались лишние файлы: %v", names)
	}
	if err := w.Abort(); err != nil {
		t.Errorf("повторный Abort: %v", err)
	}
}

func TestWriterCommitTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err == nil {
		t.Error("повторный Commit прошёл без ошибки")
	}
	if err := w.Abort(); err != nil {
		t.Errorf("Abort после Commit: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Abort после Commit удалил файл: %v", err)
	}
}

func TestCleanTemp(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.tmp")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Незавершённая запись оставляет временный файл.
	w, err := Create(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	w.f.Close()

	n, err := CleanTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("удалено %d, want 1", n)
	}
	if names := dirNames(t, dir); len(names) != 1 || names[0] != "keep.tmp" {
		t.Errorf("в папке осталось %v, want [keep.tmp]", names)
	}
}

// Обрыв записи: вспомогательный процесс без остановки перезаписывает файл,
// его убивают в случайный момент. Файл обязан остаться целым — одной из
// полных версий.
const (
	crashEnv  = "MODVAULT_FSX_CRASH_FILE"
	crashSize = 4 << 20
)

func TestCrashHelper(t *testing.T) {
	path := os.Getenv(crashEnv)
	if path == "" {
		t.Skip("вспомогательный процесс для TestWriteSurvivesKill")
	}
	chunk := make([]byte, 64<<10)
	for b := byte('B'); ; b ^= 'B' ^ 'C' {
		for i := range chunk {
			chunk[i] = b
		}
		w, err := Create(path)
		if err != nil {
			t.Fatal(err)
		}
		for written := 0; written < crashSize; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
		fmt.Println("ready")
	}
}

func TestWriteSurvivesKill(t *testing.T) {
	if testing.Short() {
		t.Skip("запускает и убивает процессы")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")

	for round := 0; round < 8; round++ {
		if err := WriteFile(path, bytes.Repeat([]byte{'A'}, crashSize)); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
		cmd.Env = append(os.Environ(), crashEnv+"="+path)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		// Ждём первой полной перезаписи, затем убиваем посреди следующих.
		ready := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "ready") {
				ready = true
				break
			}
		}
		if !ready {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal("вспомогательный процесс не начал запись")
		}
		time.Sleep(time.Duration(rand.Intn(60)) * time.Millisecond)
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		cmd.Wait()

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != crashSize {
			t.Fatalf("раунд %d: размер %d, want %d — файл обрезан", round, len(data), crashSize)
		}
		if data[0] == 'A' {
			t.Fatalf("раунд %d: файл не был перезаписан", round)
		}
		if bytes.Count(data, data[:1]) != len(data) {
			t.Fatalf("раунд %d: в файле смешаны две версии", round)
		}

		if _, err := CleanTemp(dir); err != nil {
			t.Fatal(err)
		}
		if names := dirNames(t, dir); len(names) != 1 {
			t.Fatalf("раунд %d: после CleanTemp в папке %v", round, names)
		}
	}
}
