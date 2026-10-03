// Package game — то, что ядро спрашивает у игры: где она стоит, куда класть
// файлы мода, какие служебные файлы собрать при развёртывании и как её
// запустить. Каждая игра живёт в своём подпакете.
package game

// Install — найденная установка игры.
type Install struct {
	Dir   string `json:"dir"`
	Store string `json:"store"` // «Steam», «Xbox», «вручную»
}

// Role — особая роль мода в игре.
type Role string

const (
	RoleNone      Role = ""
	RoleLoader    Role = "loader"    // загрузчик модов: без него моды не работают
	RoleFramework Role = "framework" // общая библиотека для модов
)

// Layout — как файлы мода ложатся в игру.
type Layout struct {
	Kind string // человеческое описание раскладки: «обычный мод», «оверлей»
	// Paths — путь в архиве → путь в игре; файлы, которых здесь нет, не кладутся.
	Paths map[string]string
	// Ignored — файлы архива, которые в игру не идут (описания, картинки).
	Ignored []string
	// Folders — папки модов, которые игра загружает по имени.
	Folders []string
	Role    Role
}

// ModInfo — мод профиля, как его видит игра при сборке служебных файлов.
type ModInfo struct {
	ModID   string
	Enabled bool
	Layout  Layout
}

// Generated — служебный файл, который ложится в игру вместе с модами.
type Generated struct {
	Path string // в игре
	Src  string // готовый файл на диске
}

// Level — важность замечания.
type Level string

const (
	Info  Level = "info"
	Warn  Level = "warn"
	Error Level = "error"
)

// Notice — замечание игры для пользователя.
type Notice struct {
	Level  Level
	Title  string
	Detail string
}

// Context — то, что игре нужно для сборки служебных файлов.
type Context struct {
	Dir      string // папка игры
	WorkDir  string // папка, где игра может хранить собранные файлы
	Mods     []ModInfo
	Original func(rel string) (string, error) // путь к оригиналу файла игры
}

// Game — плагин игры.
type Game interface {
	ID() string
	Name() string
	// Detect ищет установки игры на компьютере.
	Detect() ([]Install, error)
	// Validate проверяет, что папка похожа на установку игры.
	Validate(dir string) error
	// Layout раскладывает файлы архива (пути через «/») по папкам игры.
	Layout(files []string) (Layout, error)
	// Generate собирает служебные файлы: порядок загрузки, патчи.
	Generate(ctx Context) ([]Generated, []Notice, error)
	// Managers перечисляет другие менеджеры модов, найденные в папке игры.
	Managers(dir string) []string
	// Launch запускает игру.
	Launch(inst Install) error
}
