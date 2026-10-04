// Package game — то, что ядро спрашивает у игры: где она стоит, куда класть
// файлы мода, какие служебные файлы собрать при развёртывании и как её
// запустить. Каждая игра живёт в своём подпакете.
package game

import (
	"time"

	"github.com/zemidala/modvault/rules"
)

// Install — найденная установка игры.
type Install struct {
	Dir   string `json:"dir"`
	Store string `json:"store"` // «Steam», «Xbox», «вручную»
	// ViaLauncher — запускать игру через её лаунчер, а не напрямую.
	ViaLauncher bool `json:"-"`
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
	Path  string // в игре
	Src   string // готовый файл на диске
	Title string // что это за файл — для плана развёртывания
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

// OrderContext — то, что игре нужно, чтобы рассказать о порядке загрузки.
type OrderContext struct {
	Dir  string
	Mods []ModInfo
	// Read читает файл мода по его пути в игре.
	Read func(modID, gamePath string) ([]byte, error)
}

// Ordering — что известно о порядке загрузки модов.
type Ordering struct {
	// Auto — загрузчик сам упорядочивает моды и не слушает порядок из профиля.
	Auto  bool
	Rules []rules.Rule
	// LastOrder — папки модов в порядке, в котором игра загрузила их
	// в последний раз; пусто, если это неизвестно.
	LastOrder     []string
	LastOrderTime time.Time
}

// Game — плагин игры.
type Game interface {
	ID() string
	Name() string
	// NexusDomain — имя игры в адресах Nexus Mods; пусто — игры там нет.
	NexusDomain() string
	// Detect ищет установки игры на компьютере.
	Detect() ([]Install, error)
	// Validate проверяет, что папка похожа на установку игры.
	Validate(dir string) error
	// Layout раскладывает файлы архива (пути через «/») по папкам игры.
	Layout(files []string) (Layout, error)
	// Describe описывает файлы, которые уже лежат так, как в игре
	// (например, приняты у другого менеджера модов): пути не меняются.
	Describe(files []string) Layout
	// Originals перечисляет оригиналы файлов игры, которые сохранили другие
	// инструменты: путь в игре → путь к сохранённому оригиналу там же.
	Originals(dir string) map[string]string
	// Ordering сообщает правила порядка загрузки и то, кто его задаёт.
	Ordering(ctx OrderContext) Ordering
	// Generate собирает служебные файлы: порядок загрузки, патчи.
	Generate(ctx Context) ([]Generated, []Notice, error)
	// Managers перечисляет другие менеджеры модов, найденные в папке игры.
	Managers(dir string) []string
	// ModsDir — папка в игре (через «/»), где каждая подпапка — мод; пусто —
	// такой папки у игры нет.
	ModsDir() string
	// Launch запускает игру.
	Launch(inst Install) error
	// LastRun рассказывает о последнем запуске игры по её журналу: какие
	// моды выдали ошибки и упала ли она. ok ложно, если журнала нет.
	LastRun() (report RunReport, ok bool)
}

// ModErrors — ошибки одного мода в журнале игры.
type ModErrors struct {
	Mod   string // как мод назван в журнале: по папке, из которой грузится
	Count int
	First string // текст первой ошибки
}

// RunReport — что случилось в последнем запуске игры.
type RunReport struct {
	Time time.Time // когда журнал дописан в последний раз
	Log  string    // файл журнала
	// Crashed — игра упала; CrashKind и CrashText — вид и причина сбоя,
	// как их записала сама игра.
	Crashed   bool
	CrashKind string
	CrashText string
	// Mods — моды с ошибками, от самых шумных.
	Mods []ModErrors
}
