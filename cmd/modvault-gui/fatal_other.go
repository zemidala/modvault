//go:build !windows

package main

import (
	"fmt"
	"os"

	"github.com/zemidala/modvault/i18n"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, i18n.T("не удалось открыть окно программы:"), err)
	os.Exit(1)
}
