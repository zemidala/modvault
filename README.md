# Modvault

**Mod manager for Warhammer 40,000: Darktide.** This repository holds the source code.

- **Download, features, screenshots:** [modvault-releases](https://github.com/zemidala/modvault-releases) —
  a single `Modvault.exe`, no installation.
- **Bugs, ideas, questions:** [Issues](https://github.com/zemidala/modvault-releases/issues/new/choose) and
  [Discussions](https://github.com/zemidala/modvault-releases/discussions) there. English or Russian.
- **License:** [MIT](LICENSE).

Описание на русском — [ниже](#русский).

## How it works

- Mods live in a separate store, not in the game folder.
- They go into the game as hard links, so enabling, disabling and switching sets is instant.
- The program knows which file in the game belongs to which mod and can remove everything cleanly.
- Every operation either completes or is rolled back.

## Build

Go 1.27 or newer, Windows 10 or 11 (64-bit). The window uses Wails 2 and the WebView2 runtime; no Node.js is needed.

```
go test ./...
go build -o bin/modvault.exe ./cmd/modvault
go build -tags desktop,production -ldflags "-H windowsgui" -o bin/modvault-gui.exe ./cmd/modvault-gui
```

`modvault-gui.exe` is the window, `modvault.exe` is a command line used for checks and debugging.

### Release build

This is how `Modvault.exe` in the releases is built.

1. Set the version in `cmd/modvault-gui/winres/winres.json` (`version`, `file_version`,
   `product_version`, `FileVersion`, `ProductVersion`), then in `cmd/modvault-gui` run
   `go run github.com/tc-hib/go-winres@latest make --arch amd64`.
2. Build the single exe:
   ```
   go build -trimpath -tags desktop,production -ldflags "-H windowsgui -s -w -X github.com/zemidala/modvault/internal/version.Version=X.Y.Z" -o Modvault.exe ./cmd/modvault-gui
   ```
3. Tag `vX.Y.Z` here; the release with `Modvault.exe` and its SHA-256 goes to this repository and to
   `modvault-releases`.

## Layout

```
cmd/modvault      command line
cmd/modvault-gui  the window (Wails 2)
core/fsx          atomic writes, hashes, links, Recycle Bin
core/store        mod store
core/manifest     record of deployed files
core/deploy       deployment and integrity check
core/journal      operation journal and rollback
core/profile      sets (profiles)
core/archive      zip, 7z, rar archives
game              the "game" interface
game/darktide     Darktide support
rules             sorting, requirements
nexus             Nexus Mods
vortex            take over from Vortex and return
manager           logic behind the window and the command line
ui                the window: page and its link to manager
i18n              English and Russian
```

Packages under `core` know nothing about specific games.

Development plan (in Russian): [docs/PLAN.md](docs/PLAN.md).

## Third-party components

Wails (MIT), bodgit/sevenzip (BSD), nwaples/rardecode (BSD), golang.org/x/sys (BSD); fonts Forum, PT Sans and
Saira Stencil One (SIL Open Font License 1.1, texts in `ui/frontend/fonts`).

---

## Русский

**Менеджер модов для Warhammer 40,000: Darktide.** В этом репозитории — исходный код.

- **Загрузка, возможности, снимки:** [modvault-releases](https://github.com/zemidala/modvault-releases) —
  один файл `Modvault.exe`, без установки.
- **Ошибки, идеи, вопросы:** Issues и Discussions там же.
- **Лицензия:** [MIT](LICENSE).

Как устроено: моды лежат в отдельном хранилище и попадают в игру жёсткими ссылками; программа знает, какой
файл какому моду принадлежит; любая операция либо завершается целиком, либо откатывается.

Сборка и выпуск описаны выше, в разделах «Build» и «Release build». План разработки —
[docs/PLAN.md](docs/PLAN.md).
