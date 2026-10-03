// Список модов: сортировка, столбцы, фильтры, строки и карточка мода.

// Сортировка списка по столбцу: щелчок по названию столбца — по
// возрастанию, второй — по убыванию, третий — обратно к порядку загрузки.
// Это только вид списка: порядок загрузки модов в игре он не меняет.
// Избранные моды всегда стоят первыми.
let sorting = { key: "", desc: false };
try {
  sorting = JSON.parse(localStorage.getItem("modvault.sort")) || sorting;
} catch (err) {
  // сохранённой сортировки нет или она испорчена — остаётся порядок загрузки
}

const updateRank = { update: 0, unknown: 1, missing: 2, current: 3 };
const levelRank = { error: 0, warn: 1, ok: 2, off: 3 };
const byText = (a, b) => a.localeCompare(b, "ru", { numeric: true, sensitivity: "base" });

// Значение мода для сортировки по столбцу; null — значения нет, такие
// моды стоят в конце при любом направлении.
const sortValue = {
  num: (mod, index) => index,
  toggle: (mod) => (mod.enabled ? 0 : 1),
  name: (mod) => mod.name,
  author: (mod) => mod.author || null,
  category: (mod) => mod.category || null,
  installed: (mod) => Date.parse(mod.installed) || null,
  rating: (mod) => (mod.hasStats ? mod.endorsements : null),
  stats: (mod) => (mod.hasStats ? mod.uniqueDownloads : null),
  version: (mod) => (mod.version && mod.version !== "—" ? mod.version : null),
  update: (mod) => (mod.updateStatus ? updateRank[mod.updateStatus] : null),
  sets: (mod) => ((mod.sets || []).length ? mod.sets.join(", ") : null),
  state: (mod) => `${levelRank[mod.level] ?? 9} ${mod.state}`,
};

// sortMods расставляет моды для показа: избранные первыми, дальше — по
// выбранному столбцу, а при равенстве и без сортировки — по порядку загрузки.
function sortMods(shown) {
  const value = sortValue[sorting.key];
  const sign = sorting.desc ? -1 : 1;
  shown.sort((a, b) => {
    const favorite = Number(b.mod.favorite) - Number(a.mod.favorite);
    if (favorite) return favorite;
    if (value) {
      const x = value(a.mod, a.index);
      const y = value(b.mod, b.index);
      if (x === null || y === null) {
        if (x !== y) return x === null ? 1 : -1;
      } else {
        const order = typeof x === "number" ? x - y : byText(x, y);
        if (order) return order * sign;
      }
    }
    return a.index - b.index;
  });
}

// renderSortHeads показывает в шапке таблицы, по какому столбцу идёт сортировка.
function renderSortHeads() {
  for (const th of document.querySelectorAll(".mods th[data-sort]")) {
    const active = th.dataset.sort === sorting.key;
    th.setAttribute("aria-sort", !active ? "none" : sorting.desc ? "descending" : "ascending");
    th.querySelector(".sort-mark").textContent = !active ? "" : sorting.desc ? "▼" : "▲";
  }
}

function sortBy(key) {
  if (sorting.key !== key) sorting = { key, desc: false };
  else if (!sorting.desc) sorting = { key, desc: true };
  else sorting = { key: "", desc: false };
  try {
    localStorage.setItem("modvault.sort", JSON.stringify(sorting));
  } catch (err) {
    // не сохранилось — сортировка действует до закрытия окна
  }
  renderMods();
}

// Столбцы таблицы, которые можно скрыть (шестерёнка «Столбцы»). Выбор
// запоминается; «Установлен» по умолчанию скрыт.
const columns = [
  ["author", "Автор"], ["category", "Категория"], ["rating", "Рейтинг"], ["stats", "Скачали"], ["version", "Версия"],
  ["update", "Обновление"], ["sets", "Наборы"], ["installed", "Установлен"], ["state", "Состояние"],
];
let hiddenColumns = new Set(["installed"]);
try {
  const saved = JSON.parse(localStorage.getItem("modvault.columns"));
  if (Array.isArray(saved)) hiddenColumns = new Set(saved);
} catch (err) {
  // сохранённого выбора нет — остаются столбцы по умолчанию
}

// applyColumns прячет скрытые столбцы: правила пишутся в отдельный лист стилей.
function applyColumns() {
  $("column-rules").textContent = [...hiddenColumns]
    .map((key) => `.mods th[data-sort="${key}"], .mods td.${key} { display: none; }`)
    .join("\n");
}

function openColumns() {
  const items = [{ title: "Столбцы таблицы" }];
  for (const [key, label] of columns) {
    const shown = !hiddenColumns.has(key);
    items.push({
      label: (shown ? "✓ " : "　 ") + label,
      current: shown,
      keep: true,
      action: () => {
        if (shown) hiddenColumns.add(key);
        else hiddenColumns.delete(key);
        try {
          localStorage.setItem("modvault.columns", JSON.stringify([...hiddenColumns]));
        } catch (err) {
          // не сохранилось — выбор действует до закрытия окна
        }
        applyColumns();
        openColumns();
      },
    });
  }
  showMenu($("columns-button"), items);
}

// Быстрые фильтры списка: по состоянию мода и по категории.
const filterKinds = [
  ["all", "Все", () => true],
  ["enabled", "Включённые", (mod) => mod.enabled],
  ["disabled", "Выключенные", (mod) => !mod.enabled],
  ["update", "С обновлением", (mod) => !!mod.available],
  ["errors", "С ошибками", (mod) => mod.runErrors > 0],
  ["favorite", "Избранные", (mod) => !!mod.favorite],
];
let filter = { kind: "all", category: "" };

function passes(mod) {
  const kind = filterKinds.find((k) => k[0] === filter.kind) || filterKinds[0];
  return kind[2](mod) && (!filter.category || mod.category === filter.category);
}

function openCategories() {
  const counts = new Map();
  for (const mod of state.mods) if (mod.category) counts.set(mod.category, (counts.get(mod.category) || 0) + 1);
  const items = [{ title: "Категория на Nexus" }, { label: "Все категории", current: !filter.category, action: () => setFilter({ category: "" }) }];
  for (const name of [...counts.keys()].sort()) {
    items.push({ label: name, hint: String(counts.get(name)), current: filter.category === name, action: () => setFilter({ category: name }) });
  }
  if (!counts.size) items.push({ label: "Категории появятся после проверки обновлений", disabled: true });
  showMenu($("filter-category"), items);
}

function setFilter(change) {
  filter = { ...filter, ...change };
  renderMods();
}

// renderFilters рисует фильтры и пишет, сколько модов показано.
function renderFilters(shown) {
  const box = $("filter-chips");
  box.replaceChildren();
  for (const [key, label, test] of filterKinds) {
    const n = state.mods.filter(test).length;
    if (key !== "all" && n === 0) continue;
    const chip = el("button", "chip", `${label} ${n}`);
    chip.setAttribute("aria-pressed", String(filter.kind === key));
    chip.addEventListener("click", () => setFilter({ kind: key }));
    box.append(chip);
  }
  $("filter-category").textContent = (filter.category || "Категория") + " ▾";
  $("filter-category").setAttribute("aria-pressed", String(!!filter.category));
  const filtered = shown !== state.mods.length;
  $("filter-count").hidden = !filtered;
  $("filter-count").textContent = `Показано ${shown} из ${state.mods.length}`;
  $("filter-reset").hidden = !filtered;
}

function resetFilters() {
  filter = { kind: "all", category: "" };
  $("search").value = "";
  renderMods();
}

// openVersions показывает версии мода в хранилище: к прежней можно вернуться.
async function openVersions(mod, anchor) {
  let versions;
  try {
    versions = await backend().ModVersions(mod.id);
  } catch (err) {
    toast(String(err), "error");
    return;
  }
  const items = [{ title: `Версии «${mod.name}» в хранилище` }];
  for (const v of versions) {
    items.push({
      label: (v.current ? "✓ " : "") + v.version,
      hint: "добавлена " + clock(v.added),
      current: v.current,
      action: v.current ? null : () => act(() => backend().UseVersion(mod.id, v.id), $("set-button"), "Версия меняется…"),
    });
  }
  if (versions.length < 2) items.push({ label: "Других версий нет: прежняя появится после обновления", disabled: true });
  showMenu(anchor, items);
}

// Набор как файл: сохранить свой набор и загрузить чужой.
async function importSet() {
  let res;
  try {
    res = await backend().ImportSet();
  } catch (err) {
    toast(String(err), "error");
    return;
  }
  if (!res.set) return; // файл не выбран
  state = res.state;
  render();
  if (!res.missing.length) {
    toast(res.message);
    return;
  }
  const links = res.missing.filter((m) => m.url);
  const list = res.missing.map((m) => `• ${m.name}${m.version ? " " + m.version : ""}${m.url ? "" : " — нет на Nexus, ищите сами"}`).join("\n");
  const open = await ask({
    title: "Набор загружен, но модов не хватает",
    message: `${res.message}.\n\nНе хватает:\n${list}`,
    ok: links.length ? `Открыть страницы на Nexus (${Math.min(links.length, 15)})` : "",
  });
  if (open && links.length) {
    try {
      await backend().OpenPages(links.map((m) => m.url));
    } catch (err) {
      toast(String(err), "error");
    }
  }
}

function renderMods() {
  const body = $("mods");
  const query = $("search").value.trim().toLowerCase();
  body.replaceChildren();

  // Избранные моды стоят первыми, дальше — по выбранному столбцу; номер у
  // каждого — его место в порядке загрузки.
  const shown = state.mods.map((mod, index) => ({ mod, index }));
  sortMods(shown);
  renderSortHeads();
  shown.forEach(({ mod, index }) => {
    if (query && !mod.name.toLowerCase().includes(query) && !(mod.author || "").toLowerCase().includes(query)) return;
    if (!passes(mod)) return;

    const row = el("tr");
    row.tabIndex = 0;
    row.dataset.enabled = String(mod.enabled);
    row.setAttribute("aria-selected", String(mod.id === selectedId));
    row.classList.toggle("picked", picked.has(mod.id));

    const toggleCell = el("td", "tgl");
    if (mod.pinned) {
      toggleCell.append(el("span", "always", "всегда"));
    } else {
      const toggle = el("button", "switch");
      toggle.setAttribute("role", "switch");
      toggle.setAttribute("aria-checked", String(mod.enabled));
      toggle.setAttribute("aria-label", (mod.enabled ? "Выключить " : "Включить ") + mod.name);
      const track = el("span", "switch-track");
      track.append(el("span", "switch-knob"));
      toggle.append(track);
      toggle.addEventListener("click", (event) => {
        event.stopPropagation();
        setEnabled(mod.id, !mod.enabled);
      });
      toggleCell.append(toggle);
    }

    const stateCell = el("td", "state", mod.state);
    stateCell.dataset.level = mod.level;
    stateCell.title = mod.state;
    row.dataset.id = mod.id;

    row.classList.toggle("favorite", !!mod.favorite);
    const nameCell = el("td", "name");
    nameCell.title = mod.name + ((mod.sets || []).length ? " — в наборах: " + mod.sets.join(", ") : "");
    const star = el("button", "star", mod.favorite ? "★" : "☆");
    star.title = mod.favorite ? "Убрать из избранного" : "В избранное: мод будет стоять вверху списка";
    star.setAttribute("aria-label", star.title);
    star.setAttribute("aria-pressed", String(!!mod.favorite));
    star.addEventListener("click", (event) => {
      event.stopPropagation();
      call(() => backend().SetFavorite([mod.id], !mod.favorite));
    });
    nameCell.append(star, mod.name);
    if (mod.runErrors) {
      // Игра сама записала, что этот мод выдавал ошибки в прошлом запуске.
      const warn = el("span", "run-errors", `⚠ ${mod.runErrors}`);
      warn.title = `Ошибок в прошлом запуске игры: ${mod.runErrors}. Первая: ${mod.runError}`;
      nameCell.append(warn);
    }

    const categoryCell = el("td", "category", mod.category || "—");
    categoryCell.title = mod.category || (mod.nexusId ? "Категория станет известна после проверки обновлений" : "У мода нет номера на Nexus");
    const installedCell = el("td", "installed", clock(mod.installed));
    installedCell.title = "Эта версия добавлена " + new Date(mod.installed).toLocaleString("ru-RU");

    const ratingCell = el("td", "rating", mod.hasStats ? count(mod.endorsements) : "—");
    ratingCell.title = mod.hasStats ? `Одобрений на Nexus: ${mod.endorsements.toLocaleString("ru-RU")}` : statsHint(mod);
    const statsCell = el("td", "stats", mod.hasStats ? count(mod.uniqueDownloads) : "—");
    statsCell.title = mod.hasStats ? downloadsText(mod) : statsHint(mod);
    const authorCell = el("td", "author");
    authorCell.title = mod.author || (mod.nexusId ? "Автор станет известен после проверки обновлений" : "У мода нет номера на Nexus: автор неизвестен");
    authorCell.append(authorLink(mod) || mod.author || "—");
    const versionCell = el("td", "version", mod.version);
    versionCell.title = mod.version;
    const updateTd = el("td", "update");
    updateCell(updateTd, mod);

    // В каких наборах мод включён; текущий набор выделен.
    const setsCell = el("td", "sets");
    const sets = mod.sets || [];
    setsCell.title = sets.length ? "Включён в наборах: " + sets.join(", ") : "Не включён ни в одном наборе";
    sets.forEach((name, i) => {
      if (i > 0) setsCell.append(", ");
      setsCell.append(el("span", name === state.profile ? "set-current" : "", name));
    });
    if (!sets.length) setsCell.append("—");

    row.append(el("td", "num", String(index + 1)), toggleCell, nameCell, authorCell, categoryCell, ratingCell, statsCell, versionCell, updateTd, setsCell, installedCell, stateCell);

    const select = () => {
      selectedId = mod.id === selectedId ? null : mod.id;
      hideFiles();
      renderMods();
      renderCard();
    };
    row.addEventListener("click", (event) => {
      if (event.ctrlKey || event.metaKey) {
        if (!picked.delete(mod.id)) picked.add(mod.id);
        pickAnchor = mod.id;
      } else if (event.shiftKey) {
        pickRange(mod.id);
      } else {
        pickAnchor = mod.id;
        select();
        return;
      }
      renderMods();
      renderPicked();
    });
    row.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && event.target === row) select();
    });
    row.addEventListener("contextmenu", (event) => {
      event.preventDefault();
      openRowMenu(mod, event.clientX, event.clientY);
    });
    body.append(row);
  });

  $("mods-empty").hidden = body.children.length > 0;
  renderFilters(body.children.length);
}

// count сокращает большое число: 1 234 → «1,2 тыс.», 2 500 000 → «2,5 млн».
function count(n) {
  const short = (value, unit) => `${value.toFixed(value < 10 ? 1 : 0).replace(".", ",").replace(",0", "")} ${unit}`;
  if (n >= 1e6) return short(n / 1e6, "млн");
  if (n >= 1e3) return short(n / 1e3, "тыс.");
  return String(n);
}

function downloadsText(mod) {
  return `${mod.uniqueDownloads.toLocaleString("ru-RU")} человек, всего скачиваний: ${mod.downloads.toLocaleString("ru-RU")}`;
}

function statsHint(mod) {
  return mod.nexusId ? "Статистика появится после проверки обновлений" : "У мода нет номера на Nexus: статистики нет";
}

// authorLink — имя автора ссылкой на его профиль на Nexus; null, если
// профиль неизвестен.
function authorLink(mod) {
  if (!mod.author || !mod.authorUrl) return null;
  const link = el("button", "link", mod.author);
  link.title = `Открыть профиль ${mod.author} на Nexus`;
  link.addEventListener("click", async (event) => {
    event.stopPropagation(); // щелчок по ссылке не выбирает строку
    try {
      await backend().OpenAuthor(mod.id);
    } catch (err) {
      toast(String(err), "error");
    }
  });
  return link;
}

function renderCard() {
  const mod = state.mods.find((m) => m.id === selectedId);
  const card = $("card");
  card.hidden = !mod || $("view-mods").hidden;
  // С открытой карточкой списку тесно: часть столбцов уходит в неё.
  document.body.classList.toggle("with-card", !card.hidden);
  if (!mod) return;

  $("card-name").textContent = mod.name;
  $("card-source").textContent = mod.source;
  $("card-author-label").hidden = !mod.author;
  $("card-author").hidden = !mod.author;
  $("card-author").replaceChildren(authorLink(mod) || mod.author || "");
  $("card-version").textContent = mod.version;
  $("card-category-label").hidden = !mod.category;
  $("card-category").hidden = !mod.category;
  $("card-category").textContent = mod.category || "";
  $("card-installed").textContent = new Date(mod.installed).toLocaleString("ru-RU");
  $("card-versions-button").hidden = mod.versions < 2;
  for (const id of ["card-rating-label", "card-rating", "card-stats-label", "card-stats"]) $(id).hidden = !mod.hasStats;
  $("card-rating").textContent = mod.hasStats ? mod.endorsements.toLocaleString("ru-RU") : "";
  $("card-stats").textContent = mod.hasStats ? downloadsText(mod) : "";
  const sets = mod.sets || [];
  $("card-sets-label").hidden = !sets.length;
  $("card-sets").hidden = !sets.length;
  $("card-sets").textContent = sets.join(", ");
  $("card-files").textContent = String(mod.files);
  $("card-versions").textContent = String(mod.versions);
  $("card-state").textContent = mod.state;

  const hasUpdate = mod.available !== "";
  $("card-available-label").hidden = !hasUpdate;
  $("card-available").hidden = !hasUpdate;
  $("card-available").textContent = mod.available;
  $("card-update").hidden = !hasUpdate;
  $("card-update").textContent = "Обновить до " + mod.available;
  $("card-nexus").hidden = !mod.nexusId;

  const hasDeps = mod.dependsOn !== "";
  $("card-depends-label").hidden = !hasDeps;
  $("card-depends").hidden = !hasDeps;
  $("card-depends").textContent = mod.dependsOn;
}

function renderPlan() {
  $("plan").hidden = state.plan.length === 0;
  $("plan-title").textContent = state.planTitle;
  $("plan-detail").textContent = state.plan.join(" · ");
}

function hideFiles() {
  $("card-file-list").hidden = true;
  $("card-show-files").textContent = "Показать файлы";
}

async function toggleFiles() {
  const list = $("card-file-list");
  if (!list.hidden) {
    hideFiles();
    return;
  }
  try {
    const files = await backend().ModFiles(selectedId);
    list.replaceChildren(...files.map((path) => el("li", "", path)));
    list.hidden = false;
    $("card-show-files").textContent = "Скрыть файлы";
  } catch (err) {
    toast(String(err), "error");
  }
}
