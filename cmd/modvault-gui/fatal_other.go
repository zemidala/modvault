//go:build !windows

package main

import (
	"fmt"
	"os"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "не удалось открыть окно программы:", err)
	os.Exit(1)
}
