// Команда modvault — интерфейс командной строки менеджера модов.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/zemidala/modvault/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run выполняет команду и возвращает код завершения.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 0
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "неизвестная команда: %s\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `%s — менеджер модов

Использование:
  modvault <команда>

Команды:
  version   показать версию
  help      показать эту справку
`, version.String())
}
