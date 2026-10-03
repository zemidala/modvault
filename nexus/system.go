package nexus

// Keys — место, где хранится ключ API.
type Keys interface {
	// Load возвращает ключ; пустая строка — ключа нет.
	Load() (string, error)
	Save(key string) error
	Delete() error
}

// Protocol — кто в системе открывает ссылки nxm://.
type Protocol interface {
	// Command возвращает команду обработчика; пустая строка — его нет.
	Command() (string, error)
	// SetCommand назначает обработчик; пустая команда убирает его.
	SetCommand(command string) error
}

// SystemKeys — ключ в хранилище учётных данных Windows.
func SystemKeys() Keys { return credential{target: "Modvault: ключ Nexus Mods"} }

// SystemProtocol — обработчик nxm:// текущего пользователя Windows.
func SystemProtocol() Protocol { return protocol{path: `Software\Classes\nxm`} }

// MemoryKeys — ключ в памяти: для проверок и там, где системного хранилища нет.
type MemoryKeys struct{ Key string }

func (m *MemoryKeys) Load() (string, error) { return m.Key, nil }
func (m *MemoryKeys) Save(key string) error { m.Key = key; return nil }
func (m *MemoryKeys) Delete() error         { m.Key = ""; return nil }
