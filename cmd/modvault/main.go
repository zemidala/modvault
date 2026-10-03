// Команда modvault — менеджер модов в командной строке. Делает то же, что
// окно программы, над тем же хранилищем.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/zemidala/modvault/internal/version"
	"github.com/zemidala/modvault/manager"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, manager.New))
}

// Коды завершения.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// stdin — откуда команды читают ввод пользователя.
var stdin io.Reader = os.Stdin

// errUsage — команда вызвана неправильно; справка уже выведена.
var errUsage = errors.New("неверный вызов")

type cli struct {
	out, errOut io.Writer
	open        func() *manager.Manager
	m           *manager.Manager
}

func (c *cli) manager() *manager.Manager {
	if c.m == nil {
		c.m = c.open()
	}
	return c.m
}

// run выполняет команду и возвращает код завершения.
func run(args []string, stdout, stderr io.Writer, open func() *manager.Manager) int {
	if len(args) == 0 {
		usage(stdout)
		return exitOK
	}
	c := &cli{out: stdout, errOut: stderr, open: open}
	cmd, rest := args[0], args[1:]

	commands := map[string]func([]string) error{
		"status":   c.status,
		"list":     c.list,
		"add":      c.add,
		"remove":   c.remove,
		"enable":   func(a []string) error { return c.setEnabled(a, true) },
		"disable":  func(a []string) error { return c.setEnabled(a, false) },
		"move":     c.move,
		"deploy":   c.deploy,
		"verify":   c.verify,
		"check":    c.check,
		"sort":     c.sort,
		"rollback": c.rollback,
		"profile":  c.profile,
		"game":     c.game,
		"adopt":    c.adopt,
		"release":  c.release,
		"play":     c.play,
		"nexus":    c.nexus,
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.String())
		return exitOK
	case "help", "--help", "-h":
		usage(stdout)
		return exitOK
	}
	f, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(stderr, "неизвестная команда: %s\n\n", cmd)
		usage(stderr)
		return exitUsage
	}
	if err := f(rest); err != nil {
		if errors.Is(err, errUsage) {
			return exitUsage
		}
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitError
	}
	return exitOK
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `%s — менеджер модов

Использование:
  modvault <команда> [аргументы]

Состояние:
  status                    игра, хранилище, замечания, план развёртывания
  list                      моды текущего профиля в порядке загрузки
  verify                    проверить, не тронуты ли файлы модов в игре
  check                     все замечания: зависимости модов, порядок, конфликты

Моды:
  add <архив>...            добавить моды из архивов
  remove <мод> [--permanent]  удалить мод из хранилища (в Корзину)
  enable <мод>...           включить моды
  disable <мод>...          выключить моды
  move <мод> <место>        переставить мод в порядке загрузки (с 1)
  sort [--dry-run]          расставить моды по правилам их авторов

Игра:
  game [папка]              показать или выбрать папку игры
  deploy [--dry-run]        развернуть профиль в игру
  rollback [--dry-run]      вернуться к прошлому развёртыванию
  play                      запустить игру

Профили:
  profile list | use <имя> | copy <из> <в> | rename <из> <в> | delete <имя>

Vortex:
  adopt [--dry-run]         перенять управление модами у Vortex
  release [--dry-run]       вернуть управление Vortex

Nexus:
  nexus                     чей ключ сохранён
  nexus login               сохранить ключ API (читается со стандартного ввода)
  nexus logout              забыть ключ
  nexus check               проверить, вышли ли новые версии модов
  nexus get <ссылка nxm>    скачать и поставить мод по ссылке с сайта

  version, help

Мод можно указать по названию или по идентификатору из «list».
--dry-run показывает, что будет сделано, ничего не меняя.
`, version.String())
}

// flags разбирает флаги команды и возвращает остальные аргументы; флаги
// можно писать и до, и после них.
func (c *cli) flags(name string, args []string, def func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	def(fs)
	var flagArgs, rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			flagArgs = append(flagArgs, a)
		} else {
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, errUsage
	}
	return rest, nil
}

func (c *cli) need(cmd string, args []string, n int, what string) error {
	if len(args) < n {
		fmt.Fprintf(c.errOut, "использование: modvault %s %s\n", cmd, what)
		return errUsage
	}
	return nil
}

// resolve находит мод профиля по идентификатору или названию.
func (c *cli) resolve(s manager.State, ref string) (manager.Mod, error) {
	var byName []manager.Mod
	for _, m := range s.Mods {
		if m.ID == ref {
			return m, nil
		}
		if strings.EqualFold(m.Name, ref) {
			byName = append(byName, m)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return manager.Mod{}, fmt.Errorf("мод «%s» не найден; список — modvault list", ref)
	}
	return manager.Mod{}, fmt.Errorf("модов с названием «%s» несколько: укажите идентификатор", ref)
}

func (c *cli) state() (manager.State, error) {
	s, err := c.manager().State()
	if err == nil && s.Demo {
		return s, errors.New("игра не выбрана: modvault game <папка>")
	}
	return s, err
}

var levelMark = map[manager.Level]string{
	manager.LevelOK: "  ", manager.LevelWarn: "! ", manager.LevelError: "!!", manager.LevelOff: "- ",
}

func (c *cli) status(args []string) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s · профиль «%s»\n", s.Version, s.Profile)
	for _, item := range s.Status {
		fmt.Fprintf(c.out, "%s %s: %s\n", levelMark[item.Level], item.Label, item.Value)
	}
	if len(s.Issues) > 0 {
		fmt.Fprintln(c.out, "\nТребуют внимания:")
		for _, i := range s.Issues {
			fmt.Fprintf(c.out, "%s %s\n   %s\n", levelMark[i.Level], i.Title, i.Detail)
		}
	}
	if s.PlanTitle != "" {
		fmt.Fprintf(c.out, "\n%s\n", s.PlanTitle)
		for _, line := range s.Plan {
			fmt.Fprintf(c.out, "   %s\n", line)
		}
		fmt.Fprintln(c.out, "Подробно: modvault deploy --dry-run")
	}
	return nil
}

func (c *cli) list(args []string) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	if len(s.Mods) == 0 {
		fmt.Fprintln(c.out, "В профиле нет модов: modvault add <архив>")
		return nil
	}
	for i, m := range s.Mods {
		mark := "[x]"
		if !m.Enabled {
			mark = "[ ]"
		}
		fmt.Fprintf(c.out, "%3d %s %-30s %-12s %-28s %s\n", i+1, mark, m.Name, m.Version, m.State, m.ID)
	}
	return nil
}

func (c *cli) add(args []string) error {
	if err := c.need("add", args, 1, "<архив>..."); err != nil {
		return err
	}
	var failed int
	for _, path := range args {
		before, _ := c.manager().State()
		if _, err := c.manager().AddArchive(path); err != nil {
			fmt.Fprintf(c.errOut, "%s: %v\n", path, err)
			failed++
			continue
		}
		after, _ := c.manager().State()
		fmt.Fprintf(c.out, "добавлен: %s\n", newMod(before, after))
	}
	if failed > 0 {
		return fmt.Errorf("не добавлено архивов: %d", failed)
	}
	return nil
}

// newMod называет мод, который появился или сменил версию.
func newMod(before, after manager.State) string {
	had := map[string]string{}
	for _, m := range before.Mods {
		had[m.ID] = m.Version
	}
	for _, m := range after.Mods {
		if v, ok := had[m.ID]; !ok || v != m.Version {
			return m.Name + " " + m.Version
		}
	}
	return "?"
}

func (c *cli) remove(args []string) error {
	var permanent bool
	rest, err := c.flags("remove", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&permanent, "permanent", false, "удалить насовсем, минуя Корзину")
	})
	if err != nil {
		return err
	}
	if err := c.need("remove", rest, 1, "<мод> [--permanent]"); err != nil {
		return err
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	m, err := c.resolve(s, rest[0])
	if err != nil {
		return err
	}
	if _, err := c.manager().RemoveMod(m.ID, permanent); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "удалён: %s. Если он был развёрнут, его файлы уберёт следующий deploy\n", m.Name)
	return nil
}

func (c *cli) setEnabled(args []string, enabled bool) error {
	verb := map[bool]string{true: "enable", false: "disable"}[enabled]
	if err := c.need(verb, args, 1, "<мод>..."); err != nil {
		return err
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	for _, ref := range args {
		m, err := c.resolve(s, ref)
		if err != nil {
			return err
		}
		if s, err = c.manager().SetEnabled(m.ID, enabled); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s: %s\n", map[bool]string{true: "включён", false: "выключен"}[enabled], m.Name)
	}
	return nil
}

func (c *cli) move(args []string) error {
	if err := c.need("move", args, 2, "<мод> <место>"); err != nil {
		return err
	}
	pos, err := strconv.Atoi(args[1])
	if err != nil || pos < 1 {
		fmt.Fprintln(c.errOut, "место — номер в порядке загрузки, начиная с 1")
		return errUsage
	}
	s, err := c.state()
	if err != nil {
		return err
	}
	m, err := c.resolve(s, args[0])
	if err != nil {
		return err
	}
	if _, err := c.manager().Move(m.ID, pos-1); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s теперь на месте %d\n", m.Name, pos)
	return nil
}

func dryRunFlag(c *cli, name string, args []string) (bool, error) {
	var dry bool
	_, err := c.flags(name, args, func(fs *flag.FlagSet) {
		fs.BoolVar(&dry, "dry-run", false, "показать, что будет сделано, ничего не меняя")
	})
	return dry, err
}

func (c *cli) deploy(args []string) error {
	dry, err := dryRunFlag(c, "deploy", args)
	if err != nil {
		return err
	}
	if _, err := c.state(); err != nil {
		return err
	}
	if dry {
		lines, err := c.manager().PlanFiles()
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			fmt.Fprintln(c.out, "Игра уже совпадает с профилем")
			return nil
		}
		for _, line := range lines {
			fmt.Fprintln(c.out, line)
		}
		fmt.Fprintf(c.out, "\nИзменений: %d. Ничего не изменено (--dry-run)\n", len(lines))
		return nil
	}
	res, err := c.manager().Deploy()
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, res.Message)
	return nil
}

func (c *cli) verify(args []string) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	bad := 0
	for _, i := range s.Issues {
		if i.Level == manager.LevelError {
			bad++
			fmt.Fprintf(c.out, "!! %s\n   %s\n", i.Title, i.Detail)
		}
	}
	if bad > 0 {
		return fmt.Errorf("найдено проблем: %d", bad)
	}
	fmt.Fprintln(c.out, "Проблем не найдено")
	return nil
}

func (c *cli) check(args []string) error {
	s, err := c.state()
	if err != nil {
		return err
	}
	if len(s.Issues) == 0 {
		fmt.Fprintln(c.out, "Замечаний нет")
	}
	bad := 0
	for _, i := range s.Issues {
		if i.Level == manager.LevelError {
			bad++
		}
		fmt.Fprintf(c.out, "%s %s\n   %s\n", levelMark[i.Level], i.Title, i.Detail)
	}
	if s.OrderNote != "" {
		fmt.Fprintf(c.out, "\n%s\n", s.OrderNote)
	}
	if bad > 0 {
		return fmt.Errorf("найдено проблем: %d", bad)
	}
	return nil
}

func (c *cli) sort(args []string) error {
	dry, err := dryRunFlag(c, "sort", args)
	if err != nil {
		return err
	}
	if _, err := c.state(); err != nil {
		return err
	}
	plan, err := c.manager().SortPreview()
	if err != nil {
		return err
	}
	if plan.Auto {
		fmt.Fprintln(c.out, "Загрузчик модов сам расставляет их при запуске игры; список встанет как при последнем запуске")
	}
	for _, cyc := range plan.Cycles {
		fmt.Fprintf(c.out, "!  правила противоречат друг другу: %s\n", strings.Join(cyc, ", "))
	}
	if len(plan.Moves) == 0 {
		fmt.Fprintln(c.out, "Передвигать нечего: порядок уже такой")
		return nil
	}
	for _, m := range plan.Moves {
		fmt.Fprintln(c.out, m)
	}
	if dry {
		fmt.Fprintf(c.out, "\nПередвинется модов: %d. Ничего не изменено (--dry-run)\n", len(plan.Moves))
		return nil
	}
	if _, err := c.manager().Sort(); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "\nПередвинуто модов: %d. Чтобы порядок попал в игру — modvault deploy\n", len(plan.Moves))
	return nil
}

func (c *cli) rollback(args []string) error {
	dry, err := dryRunFlag(c, "rollback", args)
	if err != nil {
		return err
	}
	res, err := c.manager().Rollback(dry)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, res.Message)
	return nil
}

func (c *cli) profile(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	m := c.manager()
	switch args[0] {
	case "list":
		names, current, err := m.Profiles()
		if err != nil {
			return err
		}
		for _, n := range names {
			mark := " "
			if n == current {
				mark = "*"
			}
			fmt.Fprintf(c.out, "%s %s\n", mark, n)
		}
		return nil
	case "use":
		if err := c.need("profile use", args[1:], 1, "<имя>"); err != nil {
			return err
		}
		if err := m.UseProfile(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "текущий профиль: %s. Чтобы игра совпала с ним — modvault deploy\n", args[1])
		return nil
	case "copy", "rename":
		if err := c.need("profile "+args[0], args[1:], 2, "<из> <в>"); err != nil {
			return err
		}
		if args[0] == "copy" {
			return m.CopyProfile(args[1], args[2])
		}
		return m.RenameProfile(args[1], args[2])
	case "delete":
		if err := c.need("profile delete", args[1:], 1, "<имя>"); err != nil {
			return err
		}
		return m.DeleteProfile(args[1])
	}
	fmt.Fprintf(c.errOut, "неизвестная команда профилей: %s\n", args[0])
	return errUsage
}

func (c *cli) game(args []string) error {
	if len(args) == 0 {
		dir := c.manager().GameDir()
		if dir == "" {
			fmt.Fprintln(c.out, "игра не выбрана: modvault game <папка>")
		} else {
			fmt.Fprintln(c.out, dir)
		}
		return nil
	}
	if _, err := c.manager().SetGame(args[0]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "папка игры: %s\n", c.manager().GameDir())
	return nil
}

func (c *cli) adopt(args []string) error {
	dry, err := dryRunFlag(c, "adopt", args)
	if err != nil {
		return err
	}
	rep, err := c.manager().Adopt(dry)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Хранилище Vortex: %s\n", rep.Staging)
	fmt.Fprintf(c.out, "Модов: %d, развёрнуто: %d, файлов в игре: %d\n", rep.Mods, rep.Enabled, rep.Files)
	fmt.Fprintf(c.out, "Победителей конфликтов, закреплённых как у Vortex: %d\n", rep.Pinned)
	fmt.Fprintf(c.out, "Сохранённых оригиналов файлов игры: %d\n", rep.Originals)
	if len(rep.Others) > 0 {
		fmt.Fprintf(c.out, "Перестанут учитываться: %s\n", strings.Join(rep.Others, ", "))
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(c.out, "!  %s\n", p)
	}
	if dry {
		fmt.Fprintln(c.out, "\nНичего не изменено (--dry-run). Файлы игры при усыновлении не меняются.")
	} else {
		fmt.Fprintln(c.out, "\nУправление перенято. Вернуть Vortex: modvault release")
	}
	return nil
}

func (c *cli) release(args []string) error {
	dry, err := dryRunFlag(c, "release", args)
	if err != nil {
		return err
	}
	rep, err := c.manager().Release(dry)
	if err != nil {
		return err
	}
	if dry {
		fmt.Fprintf(c.out, "Изменится файлов в игре: %d. Ничего не изменено (--dry-run)\n", rep.Changes)
		return nil
	}
	fmt.Fprintf(c.out, "Управление возвращено Vortex (хранилище %s). Моды остались в хранилище Modvault\n", rep.Staging)
	return nil
}

func (c *cli) play(args []string) error {
	if err := c.manager().Play(); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "игра запускается")
	return nil
}

func (c *cli) nexus(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	m := c.manager()
	ctx := context.Background()
	switch args[0] {
	case "status":
		if user := m.NexusUser(); user != "" {
			fmt.Fprintf(c.out, "ключ Nexus сохранён: %s\n", user)
		} else {
			fmt.Fprintln(c.out, "ключ Nexus не задан: modvault nexus login")
		}
		return nil
	case "login":
		fmt.Fprint(c.out, "Ключ API (nexusmods.com → Site preferences → API Access → Personal API Key): ")
		key, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && key == "" {
			return errors.New("ключ не введён")
		}
		if _, err := m.NexusLogin(ctx, key); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "ключ принят и сохранён: %s\n", m.NexusUser())
		return nil
	case "logout":
		if _, err := m.NexusLogout(); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "ключ Nexus забыт")
		return nil
	case "check":
		rep, err := m.CheckUpdates(ctx)
		if err != nil {
			return err
		}
		for _, mod := range rep.State.Mods {
			if mod.Available != "" {
				fmt.Fprintf(c.out, "%-30s %s → %s\n", mod.Name, mod.Version, mod.Available)
			}
		}
		fmt.Fprintln(c.out, rep.Message)
		return nil
	case "get":
		if err := c.need("nexus get", args[1:], 1, "<ссылка nxm>"); err != nil {
			return err
		}
		shown := int64(-1)
		res, err := m.InstallLink(ctx, args[1], func(p manager.Progress) {
			if p.Total <= 0 {
				return
			}
			// Строка на каждые 10%: не забивает вывод и видна в журнале.
			if tenth := p.Done * 10 / p.Total; tenth != shown {
				shown = tenth
				fmt.Fprintf(c.out, "%s: %d%%\n", p.Name, tenth*10)
			}
		})
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, res.Message)
		return nil
	}
	fmt.Fprintf(c.errOut, "неизвестная команда Nexus: %s\n", args[0])
	return errUsage
}
