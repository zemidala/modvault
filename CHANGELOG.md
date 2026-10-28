# Changelog

<!--
Each release is a "## X.Y.Z" section: the English text, a line "---", the Russian text.
The release workflow publishes the section as the release description and adds the
SmartScreen note, the build details and the SHA-256 line itself. The English bullet
points also go to the Nexus Mods changelog.
-->

## 0.3.1

**Download `Modvault.exe` below** and replace the old exe with it — your mods and settings stay.

- New look: the header is in the brand orange without the worn texture, section titles and the main buttons use the Russo One font, and captions are brighter and easier to read.
- The **Sign in with Nexus** button is a dark plate with orange lettering instead of a bright orange one.

---

Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- Новое оформление: шапка в фирменном оранжевом без потёртостей, заголовки разделов и главные кнопки шрифтом Russo One, подписи светлее и читаются легче.
- Кнопка **«Войти через Nexus»** — тёмная плашка с оранжевой надписью вместо ярко-оранжевой.

## 0.3.0

**Download `Modvault.exe` below** and replace the old exe with it — your mods and settings stay.

- **Sign in to Nexus Mods in the browser** instead of pasting a personal API key. Click **Nexus** in the status bar: Modvault opens the Nexus page, you allow access, and the browser hands the sign-in back to Modvault. Nexus asks that apps work this way, and personal keys are meant for developers only. Modvault keeps the sign-in in Windows Credential Manager and renews it itself.
- The API key saved by earlier versions is deleted on the first start, so sign in once more after updating. **Sign out** (click your name in the status bar) ends Modvault's access on the Nexus side as well.

### Fixed
- **Take over from Vortex** stopped with "could not find the folder where Vortex keeps mods" when Vortex had deployed mods with symlinks. Vortex does that when its mod folder is on a different drive from the game (Symlink Deployment). Modvault now takes the folder from Vortex's own deployment file and follows symlinks too. If Vortex moved the files into the game instead (Move Deployment), Modvault explains how to switch Vortex to Hardlink or Symlink Deployment first.

---

Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- **Вход в Nexus Mods через браузер** вместо личного ключа API. Нажмите **Nexus** в строке состояния: Modvault откроет страницу Nexus, вы разрешите доступ, и браузер вернёт вход в Modvault. Так Nexus просит работать программы, а личные ключи у него только для разработчиков. Вход хранится в диспетчере учётных данных Windows, продлевает его Modvault сам.
- Ключ API, сохранённый прошлыми версиями, при первом запуске удаляется: после обновления войдите ещё раз. **Выход** (щелчок по своему имени в строке состояния) отзывает доступ Modvault и на стороне Nexus.

### Исправлено
- **«Перенять у Vortex»** останавливалось с ошибкой «не найдена папка, где Vortex хранит моды», если Vortex разложил моды символическими ссылками. Так он делает, когда его папка с модами лежит на другом диске, не там, где игра (Symlink Deployment). Теперь Modvault берёт папку из файла учёта самого Vortex и понимает символические ссылки. Если Vortex перенёс файлы в игру (Move Deployment), Modvault подскажет сначала переключить Vortex на Hardlink или Symlink Deployment.

## 0.2.3

**Download `Modvault.exe` below** and replace the old exe with it — your mods and settings stay.

- Mods in development and mods added from disk show their version and author from their own `.mod` file (`version = "…"`, `author = "…"`). Such values are marked with a file icon and a dotted underline: they are not from Nexus. A mod in development without an author shows your Nexus name. Raise the version in your project, and the list shows it at once.

- **Sort by rules** now also settles file conflicts where the answer is known: a mod that the rules say should override another (it loads after it or requires it — a patch, translation or add-on) is placed below it, so its files stay in the game. The preview lists these conflicts; a winner you chose yourself is never changed. The sort dialog explains what the order means when your mod loader (AML) arranges mods itself.
- A mod that **requires** another is suggested as the winner of their file conflict.

### Fixed
- With AML and DML both installed and AML chosen as the winner of `mod_manager.lua`, Modvault took DML for the loader: sorting ignored the order of the last game run and the hints were wrong.
- Rules from the `.mod` file of a mod in development (`require`, `load_after`, `load_before`) are taken into account: missing or disabled requirements are reported with an **Enable** button, sorting and **Find faulty mod** keep them in mind.
- **Open project folder** in the mod card and the folder buttons in **Settings** did nothing: the folders open in Explorer again.

---

Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- У модов в разработке и модов с диска видны версия и автор из их собственного файла `.mod` (`version = "…"`, `author = "…"`). Такие значения помечены значком файла и пунктиром: они не с Nexus. У мода в разработке без автора стоит ваше имя на Nexus. Подняли версию в проекте — список сразу показывает новую.

- **«Отсортировать по правилам»** теперь решает и конфликты файлов с известным ответом: мод, который по правилам должен перекрывать другой (грузится после него или требует его — патч, перевод, надстройка), встаёт ниже, и в игре остаются его файлы. Предпросмотр перечисляет эти конфликты; выбранного вами победителя сортировка не меняет. Окно сортировки объясняет, что значит порядок списка, когда моды расставляет сам загрузчик (AML).
- Мод, который **требует** другой, советуется победителем их конфликта файлов.

Исправлено: если стоят AML и DML и победителем файла `mod_manager.lua` выбран AML, Modvault считал загрузчиком DML — сортировка не брала порядок последнего запуска игры, подсказки были неверными. Правила из файла `.mod` мода в разработке (`require`, `load_after`, `load_before`) теперь учитываются — о недостающих и выключенных требованиях есть замечание с кнопкой **«Включить»**, их помнят сортировка и **«Найти сбойный мод»**. Кнопка **«Открыть папку проекта»** в карточке мода и кнопки папок в **«Настройках»** ничего не делали — теперь папки открываются в Проводнике.

## 0.2.2

**Download `Modvault.exe` below** and replace the old exe with it — your mods and settings stay.

- Modvault is open source now, under the MIT License: [zemidala/modvault](https://github.com/zemidala/modvault).
- Releases are built by GitHub Actions straight from the tagged source code, not on a developer's PC. Every `Modvault.exe` comes with a build provenance attestation: `gh attestation verify Modvault.exe -R zemidala/modvault` shows the exact commit it was built from.

---

Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- Код Modvault теперь открыт, лицензия MIT: [zemidala/modvault](https://github.com/zemidala/modvault).
- Выпуски собирает GitHub Actions прямо из исходников тега, а не компьютер разработчика. К каждому `Modvault.exe` приложено подтверждение происхождения: `gh attestation verify Modvault.exe -R zemidala/modvault` показывает, из какого коммита он собран.

## 0.2.1

**Download `Modvault.exe` below** and replace the old exe with it — your mods and settings stay.

- When all file conflicts are resolved, the status bar no longer shows them. They stay one click away in **Settings → Connections → Conflicts**, for example to change a winner.

---

Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- Когда все конфликты файлов разобраны, строка состояния их больше не показывает. Вернуться к ним можно в **«Настройки» → «Подключения» → «Конфликты»**, например чтобы сменить победителя.

## 0.2.0

**Download `Modvault.exe` below** — a single file (Windows 10/11 x64), no installation. Replace the old exe with it; your mods and settings stay.

### New
- **Mods outside Modvault** — mod folders you put into the game yourself are shown in the list with an “out” mark. **Take into Modvault** copies the mod into the store without touching the game: the mod appears disabled until you enable it.
- **Mods in development** — a folder link (junction) to your project becomes a mod: the game keeps the link, your edits are in the game at once, and the mod can be disabled or added to sets. The project folder is never touched.
- **Icons in the mod list** — where a mod comes from, what it requires, which mods need it, unresolved file conflicts. Selecting a mod highlights related mods; a legend sits above the list.
- **Update goes straight into the game** — no extra Deploy. The mod keeps its state; other pending changes keep waiting.
- **Ctrl + A** selects all shown mods, **Esc** clears the selection.
- **Files overwritten by Vortex are recognised** — if Vortex deploys into the game again, the notice says so instead of blaming a game update.
- Empty folders left by Vortex — a notice with **Move to Recycle Bin**.
- Service files (load order, bundle database) are named in the deployment plan instead of looking like a mod.

### Fixed
- Updates did not reach the game when the record of the last deployment listed a mod deleted since.
- Shift + click selection and drag and drop no longer pick mods outside Modvault.

---

### Что нового
Скачайте **`Modvault.exe`** ниже и замените им старый файл — моды и настройки сохранятся.

- **Моды вне Modvault** — папки модов, которые вы положили в игру сами, видны в списке с пометкой «вне». **«Взять в Modvault»** копирует мод в хранилище, не трогая игру: мод появляется выключенным, пока вы его не включите.
- **Моды в разработке** — ссылка на папку проекта (junction) становится модом: в игре остаётся ссылка, правки сразу в игре, мод можно выключать и включать в наборы. Папка проекта не трогается.
- **Значки в списке модов** — откуда мод, что ему нужно, кому нужен он, нерешённые конфликты файлов. У выбранного мода подсвечены связанные; над списком — легенда.
- **Обновление сразу в игре** — без лишнего «Развернуть». Мод остаётся в прежнем состоянии, другие ждущие изменения продолжают ждать.
- **Ctrl + A** выделяет все видные моды, **Esc** снимает выделение.
- **Файлы, перезаписанные Vortex, распознаются** — если Vortex снова развернул моды в игру, замечание так и говорит, а не винит обновление игры.
- Пустые папки от Vortex — замечание с кнопкой «Убрать в Корзину».
- Служебные файлы (порядок загрузки, база бандлов) в плане развёртывания названы по сути, а не выглядят модом.

Исправлено: обновление не доходило до игры, если в записи о последнем развёртывании оставался удалённый мод; выделение по Shift и перетаскивание больше не захватывают моды вне Modvault.

## 0.1.0

First public release.

**Download `Modvault.exe` below** — a single file (Windows 10/11 x64), no installation.

### Features
- Mods in a separate store, deployed into the game with hard links in seconds; copies on another drive
- All-or-nothing deployment with a journal: a crash or power loss rolls the game back on the next start
- Game files replaced by mods are backed up and restored; files changed outside the program are kept, never deleted
- Integrity check after game updates and Steam file checks; deployment plan by mod or by file
- Take over from Vortex in one click — and return to Vortex byte for byte
- Darktide Mod Loader and AML support: real load order from the loader's log, sorting by the mods' own rules, missing-requirement warnings, drag to reorder
- File conflict analysis with advice and winner choice
- Diagnosis after each game run from the game's console log: which mods logged errors and why the game crashed
- **Find faulty mod**: bisection finds the mod or combination of mods that breaks the game in about 10 runs for 140 mods
- Nexus Mods: Mod Manager Download links, update check, resumable verified downloads with speed, endorsements, messages to authors, collections
- Mod sets (profiles), set files to share, version rollback, favourites, filters, column chooser
- English and Russian interface, switched without a restart

The full feature list is in the [README](https://github.com/zemidala/modvault-releases#features).

---

### Первый публичный релиз
Скачайте **`Modvault.exe`** ниже — один файл, без установки.
Русский язык: **Settings → English interface** — выключите переключатель, язык сменится сразу.

Возможности: хранилище модов и развёртывание жёсткими ссылками за секунды; развёртывание «всё или ничего» с откатом после сбоя; перенять моды у Vortex и вернуть их обратно; порядок загрузки DML и AML, сортировка по правилам модов, перестановка мышью; разбор конфликтов файлов с советом; диагностика по журналу игры и **поиск сбойного мода** делением пополам; Nexus — ссылки «Mod Manager Download», проверка обновлений, загрузки с докачкой, одобрения, письма авторам, коллекции; наборы модов, откат версии, избранное, фильтры. Подробно — в [README](https://github.com/zemidala/modvault-releases#русский).
