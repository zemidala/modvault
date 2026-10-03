// Package version хранит сведения о версии программы.
package version

// Version подставляется при сборке релиза через
// -ldflags "-X github.com/zemidala/modvault/internal/version.Version=1.2.3".
var Version = "0.0.0-dev"

// Name — имя программы для вывода и заголовков запросов.
const Name = "modvault"

// String возвращает строку вида "modvault 0.0.0-dev".
func String() string {
	return Name + " " + Version
}
