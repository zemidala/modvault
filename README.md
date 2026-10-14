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

Releases are built by GitHub Actions ([release.yml](.github/workflows/release.yml)), not on a developer's PC.
A tag `vX.Y.Z` on a commit in `main` starts the workflow: it checks that the version matches, runs the tests,
builds `Modvault.exe`, attests its provenance, publishes the release here and in `modvault-releases`, and
uploads the new version to [Nexus Mods](https://www.nexusmods.com/warhammer40kdarktide/mods/1411).
A tag with a suffix (`vX.Y.Z-rc1`) makes a pre-release on GitHub only.

To prepare a release:

1. Set the version in `cmd/modvault-gui/winres/winres.json` (`version`, `file_version`,
   `product_version`, `FileVersion`, `ProductVersion`), then in `cmd/modvault-gui` run
   `go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64`.
2. Add a `## X.Y.Z` section to [CHANGELOG.md](CHANGELOG.md): English, a line `---`, Russian.
3. Commit to `main`, tag `vX.Y.Z` and push the tag.

#### Checking a release

Every `Modvault.exe` has a build provenance attestation. With the [GitHub CLI](https://cli.github.com/):

```
gh attestation verify Modvault.exe -R zemidala/modvault
```

It shows the workflow run and the commit the file was built from. The same file is in the Nexus Mods
archive; its SHA-256 is in the release description.

To build it yourself, check out the release tag and use Go 1.27.0 (the version pinned in the workflow):

```
go build -trimpath -tags desktop,production -ldflags "-H windowsgui -s -w -X github.com/zemidala/modvault/internal/version.Version=X.Y.Z" -o Modvault.exe ./cmd/modvault-gui
```

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

Сборка и выпуск описаны выше, в разделах «Build» и «Release build». Выпуски собирает GitHub Actions из
исходников тега; проверить файл: `gh attestation verify Modvault.exe -R zemidala/modvault`. План разработки —
[docs/PLAN.md](docs/PLAN.md).
