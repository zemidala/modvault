package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"без аргументов", nil, 0, "Использование:", ""},
		{"version", []string{"version"}, 0, "modvault ", ""},
		{"help", []string{"help"}, 0, "Команды:", ""},
		{"неизвестная команда", []string{"nope"}, 2, "", "неизвестная команда: nope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Fatalf("код = %d, want %d", code, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, нет %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, нет %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
