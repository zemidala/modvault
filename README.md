# Modvault

Менеджер модов для Warhammer 40,000: Darktide. Код закрыт; загрузки, ошибки и
обсуждения — в публичном репозитории
[modvault-releases](https://github.com/zemidala/modvault-releases), там же описаны
все возможности (по-английски и по-русски).

## Идея

- Моды лежат в отдельном хранилище, а не в папке игры.
- В игру они попадают жёсткими ссылками, поэтому включение, выключение и смена набора мгновенны.
- Программа знает, какой файл в игре какому моду принадлежит, и может всё чисто убрать.
- Любая операция либо завершается целиком, либо откатывается.

## Сборка

Нужен Go 1.27 или новее.

```
go test ./...
go build -o bin/modvault.exe ./cmd/modvault
go build -tags desktop,production -ldflags "-H windowsgui" -o bin/modvault-gui.exe ./cmd/modvault-gui
```

## Выпуск

1. Версия в `cmd/modvault-gui/winres/winres.json` (`version`, `file_version`,
   `product_version`, `FileVersion`, `ProductVersion`), затем в папке
   `cmd/modvault-gui`: `go run github.com/tc-hib/go-winres@latest make --arch amd64`.
2. Сборка одного exe:
   ```
   go build -trimpath -tags desktop,production -ldflags "-H windowsgui -s -w -X github.com/zemidala/modvault/internal/version.Version=X.Y.Z" -o Modvault.exe ./cmd/modvault-gui
   ```
3. Тег `vX.Y.Z` здесь и релиз с `Modvault.exe` и SHA-256 — здесь и в
   `modvault-releases`. README там описывает возможности на двух языках.

Командная строка (`cmd/modvault`) в релиз не входит — она для проверки и отладки.

## Структура

```
cmd/modvault      командная строка
cmd/modvault-gui  окно (Wails 2)
core/fsx          атомарная запись, хеши, ссылки, Корзина
core/store        хранилище модов
core/manifest     учёт развёрнутых файлов
core/deploy       развёртывание и проверка целостности
core/journal      журнал операций и откат
core/profile      наборы (профили)
core/archive      архивы zip, 7z, rar
game              интерфейс «игра»
game/darktide     поддержка Darktide
rules             сортировка, зависимости
nexus             Nexus Mods
vortex            перенять у Vortex и вернуть
manager           логика окна и командной строки
ui                окно: страница и связь с manager
i18n              английский и русский
```

Пакеты `core` ничего не знают о конкретных играх.

План разработки: [docs/PLAN.md](docs/PLAN.md).
