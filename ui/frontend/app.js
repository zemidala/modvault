// Страница главного окна. Данные приходят из Go (пакет ui), здесь только показ.
"use strict";

const $ = (id) => document.getElementById(id);

let state = null;
let selectedId = null;

// Go-сторона окна. Вне программы (страница открыта в браузере) её нет.
function backend() {
  return window.go && window.go.ui && window.go.ui.App;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}


let toastTimer = 0;
function toast(text, level, sticky) {
  const node = $("toast");
  node.textContent = text;
  node.dataset.level = level || "info";
  node.hidden = false;
  clearTimeout(toastTimer);
  // Ошибку читают дольше; сообщение о сбое запуска не гаснет вовсе.
  // Длинное сообщение тоже читают дольше.
  if (!sticky) toastTimer = setTimeout(() => { node.hidden = true; }, level === "error" ? 8000 : Math.max(3500, text.length * 60));
}

// call выполняет запрос к программе, который возвращает новое состояние окна.
async function call(request) {
  try {
    state = await request();
    render();
    return true;
  } catch (err) {
    toast(String(err), "error");
    return false;
  }
}

// Кнопка, за которой ещё нет ядра, честно говорит, когда заработает.
function notYet(label, stage) {
  toast(`«${label}» появится на этапе ${stage}`);
}

function renderStatus() {
  const box = $("status");
  box.replaceChildren();
  for (const item of state.status) {
    const node = el("span", "status-item");
    node.dataset.level = item.level;
    let value = el("span", "status-value", item.value);
    value.title = item.value;
    if (item.command) {
      value = el("button", "status-value status-command", item.value);
      value.title = item.value + " — щёлкните, чтобы изменить";
      value.addEventListener("click", () => run(item.command));
    }
    node.append(el("span", "muted", item.label), value);
    box.append(node);
  }
}

function renderIssues() {
  const box = $("issues");
  box.replaceChildren();
  for (const issue of state.issues) {
    const row = el("div", "issue");
    row.dataset.level = issue.level;
    const text = el("div", "issue-text");
    text.append(el("div", "issue-title", issue.title), el("div", "issue-detail", issue.detail));
    row.append(text);
    if (issue.action) {
      const button = el("button", "ghost", issue.action);
      button.addEventListener("click", () => (issue.command ? run(issue.command, issue.arg) : notYet(issue.action, issue.stage)));
      row.append(button);
    }
    box.append(row);
  }
  $("issues-section").hidden = state.issues.length === 0;
}

// Проверка обновлений. checking — общий ход, пока проверка идёт. marks —
// отметка у каждого мода: "queued" (в очереди), "checking" (спрашиваем
// сейчас), "ok", "missing" или найденная версия; после проверки отметки
// остаются в столбце «Обновление» до следующей.
let checking = null;
const marks = new Map();

// updateCell заполняет ячейку «Обновление» мода: значок и подпись.
function updateCell(cell, mod) {
  const mark = marks.get(mod.id);
  let text = "";
  let level = "off";
  let hint = "";
  let update = ""; // версия, до которой можно обновить
  // Пока идёт проверка, у мода её отметка; иначе — то, что известно с
  // прошлой проверки (оно переживает перезапуск программы).
  let status = mod.updateStatus;
  if (mark === "checking" || mark === "queued" || mark === "missing") status = mark;
  else if (mark === "ok") status = mod.available ? "update" : "current";
  else if (mark) status = "update";

  if (!mod.nexusId) {
    text = "—";
    hint = "У мода нет номера на Nexus: проверить его нельзя";
  } else if (status === "checking") {
    text = "Проверяется";
    level = "busy";
  } else if (status === "queued") {
    text = "В очереди";
    level = "queued";
  } else if (status === "missing") {
    text = "Нет на Nexus";
    hint = "Страница мода на Nexus убрана или скрыта автором";
  } else if (status === "update") {
    update = mark && mark !== "ok" ? mark : mod.available;
    text = update;
    level = "warn";
    hint = "На Nexus есть версия " + update;
  } else if (status === "current") {
    text = "✓ Актуально";
    level = "ok";
    hint = "Установлена последняя версия с Nexus";
  } else {
    text = "Не проверен";
    hint = "Нажмите «Проверить обновления»";
  }
  cell.dataset.level = level;
  cell.title = hint;
  cell.replaceChildren();
  if (level === "busy" || level === "queued") cell.append(el("span", "spinner"));
  // Пока идёт проверка, обновлять рано: кнопка появляется с её итогом.
  if (update && !checking) cell.append(updateButton(mod, update));
  cell.append(text);
}

// updateButton — значок «обновить» перед новой версией в строке мода.
function updateButton(mod, version) {
  const button = el("button", "icon-button");
  button.title = `Обновить «${mod.name}» до ${version}`;
  button.setAttribute("aria-label", button.title);
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(ns, "path");
  // Стрелка вниз в лоток: «скачать новую версию».
  path.setAttribute("d", "M8 2v7.5M4.5 6.5 8 10l3.5-3.5M3 13h10");
  svg.append(path);
  button.append(svg);
  button.addEventListener("click", (event) => {
    event.stopPropagation(); // щелчок по значку не выбирает строку
    act(() => backend().UpdateMod(mod.id), button, "Обновление: запрос к Nexus…");
  });
  return button;
}

// setMark ставит моду отметку и сразу показывает её в его строке.
function setMark(id, mark) {
  marks.set(id, mark);
  const mod = state.mods.find((m) => m.id === id);
  const row = document.querySelector(`#mods tr[data-id="${CSS.escape(id)}"]`);
  if (mod && row) updateCell(row.querySelector(".update"), mod);
}

// renderCheck показывает общий ход проверки обновлений над списком.
function renderCheck() {
  const box = $("check-progress");
  box.hidden = !checking;
  if (!checking) return;
  const known = checking.total > 0;
  $("check-label").textContent = checking.name ? "Проверка обновлений: " + checking.name : "Проверка обновлений: запрос к Nexus…";
  $("check-count").textContent = known ? `${checking.done} из ${checking.total}` : "";
  $("check-bar").classList.toggle("indeterminate", !known);
  $("check-fill").style.width = known ? `${Math.round((checking.done * 100) / checking.total)}%` : "";
}

// onCheckStep принимает шаг проверки от программы.
function onCheckStep(step) {
  if (!checking) return;
  checking.total = step.total;
  if (step.queued || step.unchanged) {
    // Расклад: кто с прошлой проверки не менялся, тот уже проверен.
    for (const id of step.unchanged || []) setMark(id, "ok");
    for (const id of step.queued || []) setMark(id, "queued");
  } else {
    checking.done = step.done;
    checking.name = step.name;
    const mark = !step.finished ? "checking" : step.missing ? "missing" : step.available || "ok";
    for (const id of step.mods || []) setMark(id, mark);
  }
  renderCheck();
}

// checkUpdates проверяет обновления. quiet — проверка идёт сама, в фоне
// (при запуске программы): окно не занято, ход виден в столбце
// «Обновление», а сообщение появляется, только если есть что сказать.
async function checkUpdates(quiet) {
  if (checking) return;
  checking = { done: 0, total: 0, name: "" };
  marks.clear();
  for (const mod of state.mods) if (mod.nexusId) marks.set(mod.id, "queued");
  renderCheck();
  renderMods();
  const button = $("check-updates");
  button.disabled = true;
  button.classList.add("busy");
  try {
    const res = await backend().CheckUpdates();
    state = res.state;
    if (!quiet || res.updates > 0) toast(res.message);
  } catch (err) {
    toast((quiet ? "Проверка обновлений при запуске не удалась: " : "") + String(err), "error");
  } finally {
    button.disabled = false;
    button.classList.remove("busy");
    checking = null;
    // Итог проверки теперь в состоянии каждого мода: отметки больше не нужны.
    marks.clear();
    render();
    renderCheck();
  }
}

// Выделение нескольких модов (Ctrl и Shift + щелчок) для действий с ними
// разом. Это не то же, что выбранный мод, чью карточку показывает окно.
const picked = new Set();
let pickAnchor = null; // от какой строки тянется выделение с Shift

function pickRange(toId) {
  const ids = [...document.querySelectorAll("#mods tr")].map((row) => row.dataset.id);
  const from = ids.indexOf(pickAnchor ?? selectedId ?? ids[0]);
  const to = ids.indexOf(toId);
  if (from < 0 || to < 0) return;
  for (let i = Math.min(from, to); i <= Math.max(from, to); i++) picked.add(ids[i]);
}

function clearPicked() {
  picked.clear();
  pickAnchor = null;
  renderMods();
  renderPicked();
}

// renderPicked показывает полоску действий с выделенными модами.
function renderPicked() {
  const n = picked.size;
  $("picked-bar").hidden = n === 0;
  $("pick-hint").hidden = n > 0 || state.demo || state.mods.length < 2;
  const word = n % 10 === 1 && n % 100 !== 11 ? "мод" : n % 10 >= 2 && n % 10 <= 4 && (n % 100 < 12 || n % 100 > 14) ? "мода" : "модов";
  $("picked-count").textContent = `Выделено: ${n} ${word}`;
}

// Всплывающее меню под кнопкой anchor или в точке {x, y}. Пункт: {label, hint, current, disabled,
// action, remove, removeTitle}, заголовок {title} или черта {separator}.
function showMenu(anchor, items) {
  const menu = $("menu");
  menu.replaceChildren();
  for (const item of items) {
    if (item.separator) {
      menu.append(el("div", "menu-separator"));
      continue;
    }
    if (item.title) {
      menu.append(el("div", "menu-title", item.title));
      continue;
    }
    const row = el("div", "menu-row");
    const button = el("button", "menu-item");
    button.setAttribute("role", "menuitem");
    button.append(el("span", "menu-label", item.label));
    if (item.hint) button.append(el("span", "menu-hint", item.hint));
    if (item.current) button.setAttribute("aria-current", "true");
    button.disabled = !!item.disabled;
    button.addEventListener("click", () => {
      hideMenu();
      if (item.action) item.action();
    });
    row.append(button);
    if (item.remove) {
      const remove = el("button", "menu-remove", "✕");
      remove.title = item.removeTitle;
      remove.setAttribute("aria-label", item.removeTitle);
      remove.addEventListener("click", () => {
        hideMenu();
        item.remove();
      });
      row.append(remove);
    }
    menu.append(row);
  }
  menu.hidden = false;
  // Меню открывается под кнопкой или в точке щелчка и не вылезает за окно.
  const box = anchor.getBoundingClientRect ? anchor.getBoundingClientRect() : null;
  const left = box ? box.left : anchor.x;
  const top = box ? box.bottom + 4 : anchor.y;
  menu.style.left = `${Math.max(8, Math.min(left, window.innerWidth - menu.offsetWidth - 8))}px`;
  menu.style.top = `${Math.max(8, Math.min(top, window.innerHeight - menu.offsetHeight - 8))}px`;
  menu.dataset.anchor = anchor.id || "";
  const first = menu.querySelector("button:not(:disabled)");
  if (first) first.focus();
}

function hideMenu() {
  $("menu").hidden = true;
}

// Наборы модов: меню в шапке и действия с выделенными модами.
async function loadSets() {
  try {
    return await backend().Sets();
  } catch (err) {
    toast(String(err), "error");
    return null;
  }
}

async function openSets() {
  const sets = await loadSets();
  if (!sets) return;
  const items = [{ title: "Наборы модов" }];
  for (const set of sets) {
    items.push({
      label: (set.current ? "✓ " : "") + set.name,
      hint: `включено: ${set.enabled}`,
      current: set.current,
      action: set.current ? null : () => switchSet(set.name),
      remove: set.current ? null : () => call(() => confirmThen(() => backend().DeleteSetAsk(set.name), () => backend().DeleteSet(set.name))),
      removeTitle: `Удалить набор «${set.name}»`,
    });
  }
  items.push({ separator: true });
  items.push({ label: "Новый набор — копия текущего…", action: () => newSet([]) });
  items.push({ label: `Переименовать «${state.profile}»…`, action: renameSet });
  showMenu($("set-button"), items);
}

// switchSet выбирает набор; программа сразу приводит к нему игру.
async function switchSet(name) {
  picked.clear();
  await act(() => backend().SwitchSet(name), $("set-button"), `Набор «${name}»: игра приводится к нему…`);
  renderPicked();
}

async function newSet(ids) {
  const name = await ask(await backend().NewSetAsk(ids.length));
  if (name === null) return;
  // Выделение снимается только если набор создан: при ошибке его не придётся собирать заново.
  if (await act(() => backend().CreateSet(name, ids), $("set-button"))) clearPicked();
}

async function renameSet() {
  const from = state.profile;
  const to = await ask(await backend().RenameSetAsk(from));
  if (to === null || to.trim() === from) return;
  call(() => backend().RenameSet(from, to));
}

async function openAddToSet() {
  const sets = await loadSets();
  if (!sets) return;
  const ids = [...picked];
  const items = [{ title: "Включить выделенные моды в наборе" }];
  for (const set of sets) {
    if (set.current) continue;
    items.push({
      label: set.name,
      hint: `включено: ${set.enabled}`,
      action: async () => {
        if (await act(() => backend().AddToSet(set.name, ids), $("picked-add"))) clearPicked();
      },
    });
  }
  if (items.length === 1) items.push({ label: "Других наборов пока нет", disabled: true });
  items.push({ separator: true });
  items.push({ label: "В новый набор…", action: () => newSet(ids) });
  showMenu($("picked-add"), items);
}

async function setPickedEnabled(enabled) {
  const ids = [...picked];
  if (await call(() => backend().SetEnabledMany(ids, enabled))) clearPicked();
}

// Меню мода по правой кнопке мыши: убрать из набора, добавить или
// перенести в другой. Если мод входит в выделение, действие относится ко
// всем выделенным.
async function openRowMenu(mod, x, y) {
  const many = picked.has(mod.id) && picked.size > 1;
  const ids = many ? [...picked] : [mod.id];
  const done = async (request) => {
    if ((await act(request, $("set-button"))) && many) clearPicked();
  };
  const current = state.profile;
  const items = [{ title: many ? `Выделено модов: ${ids.length}` : mod.name }];
  if (many || !mod.favorite) items.push({ label: "★ В избранное", action: () => call(() => backend().SetFavorite(ids, true)) });
  if (many || mod.favorite) items.push({ label: "☆ Убрать из избранного", action: () => call(() => backend().SetFavorite(ids, false)) });
  items.push({ separator: true });
  if (many || mod.enabled) {
    items.push({ label: `Убрать из набора «${current}»`, action: () => done(() => backend().RemoveFromSet(current, ids)) });
  }
  if (many || !mod.enabled) {
    items.push({ label: `Включить в наборе «${current}»`, action: () => done(() => backend().AddToSet(current, ids)) });
  }

  const others = ((await loadSets()) || []).filter((set) => !set.current);
  if (others.length) {
    items.push({ separator: true }, { title: "Добавить в набор" });
    for (const set of others) {
      const inSet = !many && (mod.sets || []).includes(set.name);
      items.push(inSet
        ? { label: `✓ ${set.name}`, hint: "убрать", action: () => done(() => backend().RemoveFromSet(set.name, ids)) }
        : { label: set.name, action: () => done(() => backend().AddToSet(set.name, ids)) });
    }
    // Перенести — убрать из текущего набора и включить в другом.
    if (many || mod.enabled) {
      items.push({ separator: true }, { title: `Перенести из «${current}» в набор` });
      for (const set of others) {
        items.push({ label: set.name, action: () => done(() => backend().MoveToSet(set.name, ids)) });
      }
    }
  }
  items.push({ separator: true });
  items.push({ label: "В новый набор…", action: () => newSet(ids) });
  showMenu({ x, y }, items);
}

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

    const row = el("tr");
    row.tabIndex = 0;
    row.dataset.enabled = String(mod.enabled);
    row.setAttribute("aria-selected", String(mod.id === selectedId));
    row.classList.toggle("picked", picked.has(mod.id));

    const toggleCell = el("td");
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

    row.append(el("td", "num", String(index + 1)), toggleCell, nameCell, authorCell, ratingCell, statsCell, versionCell, updateTd, setsCell, stateCell);

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

function render() {
  if (!state.mods.some((m) => m.id === selectedId)) {
    selectedId = null;
    hideFiles();
  }
  for (const id of picked) if (!state.mods.some((m) => m.id === id)) picked.delete(id);
  $("profile").textContent = state.profile;
  $("version").textContent = state.version;
  $("demo").hidden = !state.demo;
  $("order-note").hidden = !state.orderNote;
  $("order-note").textContent = state.orderNote || "";
  $("home").hidden = state.demo;
  $("home").textContent = "Хранилище: " + state.home;
  renderStatus();
  renderIssues();
  renderMods();
  renderCard();
  renderPlan();
  renderPicked();
  renderSetup();
  renderBisect();
  if (currentTab === "settings") renderSettings();
  if (currentTab === "journal") renderJournal();
  if (currentTab === "downloads") renderDownloads();
}

// ask показывает вопрос в окне программы и ждёт ответа: true — действие
// подтверждено. Esc и «Отмена» — отказ. У вопроса с полем ввода ответ —
// введённая строка, а отказ — null.
function ask(question) {
  return new Promise((resolve) => {
    const box = $("ask");
    $("ask-title").textContent = question.title;
    $("ask-message").textContent = question.message;
    const input = $("ask-input");
    const refuse = question.input || (question.choices || []).length ? null : false;
    $("ask-field").hidden = !question.input;
    $("ask-input-label").textContent = question.placeholder || "";
    // Вопрос с вариантами решается выбором одного из них.
    const choices = $("ask-choices");
    choices.hidden = !(question.choices || []).length;
    choices.replaceChildren();
    for (const choice of question.choices || []) {
      const button = el("button", "ghost choice", (choice.current ? "✓ " : "") + choice.label);
      if (choice.current) button.setAttribute("aria-current", "true");
      button.addEventListener("click", () => done(choice.value));
      choices.append(button);
    }
    input.type = question.secret ? "password" : "text";
    input.value = question.value || "";
    const ok = $("ask-ok");
    // Без подписи действия вопрос только сообщает: остаётся одна кнопка.
    ok.hidden = !question.ok;
    ok.textContent = question.ok || "";
    ok.classList.toggle("danger", !!question.danger);
    $("ask-cancel").textContent = question.ok ? "Отмена" : "Закрыть";
    const done = (answer) => {
      box.hidden = true;
      box.removeEventListener("keydown", onKey);
      ok.onclick = null;
      $("ask-cancel").onclick = null;
      input.value = ""; // введённый ключ на странице не остаётся
      resolve(answer);
    };
    const onKey = (event) => {
      if (event.key === "Escape") done(refuse);
      else if (event.key === "Enter" && event.target === input) done(input.value);
    };
    ok.onclick = () => done(question.input ? input.value : true);
    $("ask-cancel").onclick = () => done(refuse);
    box.addEventListener("keydown", onKey);
    box.hidden = false;
    // Необратимое по умолчанию не выбрано: Enter без раздумий его не запустит.
    if (question.input) {
      input.focus();
      input.select();
    } else (question.danger || !question.ok ? $("ask-cancel") : ok).focus();
  });
}

// confirmThen спрашивает вопрос, полученный от программы, и при согласии
// выполняет действие; при отказе состояние остаётся прежним.
async function confirmThen(question, action) {
  if (!(await ask(await question()))) return state;
  return action();
}

// Команды, которые программа разрешает вызывать из строк состояния и замечаний.
const commands = {
  ChooseGame: () => backend().ChooseGame(),
  Adopt: () => confirmThen(() => backend().AdoptAsk(), () => backend().Adopt()),
  Release: () => confirmThen(() => backend().ReleaseAsk(), () => backend().Release()),
  IgnoreManagers: () => confirmThen(() => backend().IgnoreManagersAsk(), () => backend().IgnoreManagers()),
  Sort: () => confirmThen(() => backend().SortAsk(), () => backend().Sort()),
  EnableMod: (id) => backend().SetEnabled(id, true),
  DisableMod: (id) => backend().SetEnabled(id, false),
  NexusKey: async () => {
    const key = await ask(await backend().NexusKeyAsk());
    return key === null ? state : backend().NexusLogin(key);
  },
  ToggleNxm: () => confirmThen(() => backend().NxmAsk(), () => backend().ToggleNxm()),
  ChooseWinner: async (key) => {
    const winner = await ask(await backend().WinnerAsk(key));
    return winner === null ? state : backend().SetWinner(key, winner);
  },
};

function run(command, arg) {
  if (commands[command]) call(() => commands[command](arg));
}

// act выполняет запрос, который возвращает новое состояние и сообщение.
// Пока он идёт, кнопка не нажимается и по ней бежит полоска, а внизу висит
// строка busy: долгое действие не должно выглядеть зависшим. Возвращает,
// удался ли запрос.
async function act(request, button, busy) {
  button.disabled = true;
  button.classList.add("busy");
  if (busy) toast(busy, "busy", true);
  try {
    const res = await request();
    state = res.state;
    render();
    if (res.message) toast(res.message);
    return true;
  } catch (err) {
    toast(String(err), "error");
    return false;
  } finally {
    button.disabled = false;
    button.classList.remove("busy");
    // Строка «идёт работа» не остаётся висеть, если её ничто не сменило.
    if ($("toast").dataset.level === "busy") $("toast").hidden = true;
  }
}

// listen подписывает страницу на то, что программа делает сама: загрузки
// по ссылкам с сайта Nexus.
function listen() {
  const events = window.runtime;
  if (!events) return;
  events.EventsOn("download", (p) => {
    const share = p.total > 0 ? ` — ${Math.floor((p.done * 100) / p.total)}%` : "";
    toast(`Загрузка: ${p.name}${share}`, "busy", true);
    if (currentTab === "downloads") renderDownloads();
  });
  events.EventsOn("checking", onCheckStep);
  events.EventsOn("installed", (res) => {
    state = res.state;
    render();
    toast(res.message);
  });
  events.EventsOn("install-failed", (message) => toast(message, "error"));
}

async function deploy() {
  const button = $("deploy");
  button.disabled = true;
  button.classList.add("busy");
  toast("Развёртывание…", "busy", true);
  try {
    const res = await backend().Deploy();
    state = res.state;
    render();
    toast(res.message);
  } catch (err) {
    toast(String(err), "error");
  } finally {
    button.disabled = false;
    button.classList.remove("busy");
    if ($("toast").dataset.level === "busy") $("toast").hidden = true;
  }
}

async function showPlanFiles() {
  try {
    const lines = await backend().PlanFiles();
    $("sheet-list").replaceChildren(...lines.map((line) => el("li", "", line)));
    $("sheet").hidden = false;
    $("sheet-close").focus();
  } catch (err) {
    toast(String(err), "error");
  }
}

function setEnabled(id, enabled) {
  return call(() => backend().SetEnabled(id, enabled));
}

// Поиск сбойного мода делением пополам: программа включает половину
// модов, пользователь запускает игру и отвечает, осталась ли проблема.
function renderBisect() {
  const b = state.bisect;
  $("bisect-section").hidden = !b;
  $("bisect-start").hidden = !!b || state.demo;
  if (!b) return;
  $("bisect-title").textContent = `Шаг ${b.step} из ${b.steps}: под подозрением ${b.suspects}, сейчас включено ${b.testing.length}`;
  $("bisect-detail").textContent = "Запустите игру и посмотрите, повторяется ли проблема. Потом ответьте здесь — программа сузит круг. Включены: " + b.testing.join(", ") + ".";
}

async function bisectStep(request, button) {
  button.disabled = true;
  button.classList.add("busy");
  toast("Поиск сбойного мода: игра приводится к следующему шагу…", "busy", true);
  let res = null;
  try {
    res = await request();
    state = res.state;
    render();
    toast(res.message);
  } catch (err) {
    toast(String(err), "error");
  } finally {
    button.disabled = false;
    button.classList.remove("busy");
  }
  // Виновник найден: предложить сразу выключить его в своём наборе.
  if (res && res.culprit) {
    const yes = await ask({
      title: "Сбойный мод найден",
      message: `Проблему вызывает «${res.culpritName}».\n\nВыключить его в наборе «${state.profile}»? Остальные моды останутся как были.\n\nЕсли после этого проблема не уйдёт, виновников несколько: запустите поиск ещё раз.`,
      ok: "Выключить",
    });
    if (yes) call(() => backend().RemoveFromSet(state.profile, [res.culprit]).then((r) => r.state));
  }
}

async function startBisect() {
  if (!(await ask(await backend().BisectAsk()))) return;
  bisectStep(() => backend().StartBisect(), $("bisect-start"));
}

// Разделы окна: моды, загрузки, журнал, настройки.
let currentTab = "mods";
let downloadsTimer = 0;

const eventKinds = {
  deploy: "Игра", install: "Мод", remove: "Мод", set: "Набор", nexus: "Nexus", vortex: "Vortex", error: "Ошибка",
};

function clock(iso) {
  const d = new Date(iso);
  const two = (n) => String(n).padStart(2, "0");
  return `${two(d.getDate())}.${two(d.getMonth() + 1)} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

function megabytes(n) {
  return `${(n / 1048576).toFixed(n < 10485760 ? 1 : 0).replace(".", ",")} МБ`;
}

// renderDownloads показывает загрузки этого запуска программы.
async function renderDownloads() {
  let list;
  try {
    list = await backend().Downloads();
  } catch (err) {
    toast(String(err), "error");
    return;
  }
  const box = $("downloads");
  box.replaceChildren();
  for (const d of list) {
    const row = el("div", "download");
    row.dataset.state = d.state;
    const head = el("div", "download-head");
    head.append(el("span", "download-name", d.version ? `${d.name} ${d.version}` : d.name));
    const share = d.total > 0 ? `${megabytes(d.done)} из ${megabytes(d.total)}` : d.done > 0 ? megabytes(d.done) : "";
    const status = d.state === "active" ? `Идёт · ${share}` : d.state === "done" ? `Готово · ${clock(d.finished)}` : `Не удалось · ${clock(d.finished)}`;
    head.append(el("span", "download-status", status));
    row.append(head);
    if (d.state === "active") {
      const bar = el("div", "bar");
      const fill = el("div", "bar-fill");
      if (d.total > 0) fill.style.width = `${Math.round((d.done * 100) / d.total)}%`;
      else bar.classList.add("indeterminate");
      bar.append(fill);
      row.append(bar);
    } else if (d.message) {
      row.append(el("div", "download-message", d.message));
    }
    box.append(row);
  }
  $("downloads-empty").hidden = list.length > 0;
  $("downloads").hidden = list.length === 0;
  // Пока что-то качается, список обновляется сам.
  clearTimeout(downloadsTimer);
  if (currentTab === "downloads" && list.some((d) => d.state === "active")) {
    downloadsTimer = setTimeout(renderDownloads, 500);
  }
}

// renderJournal показывает журнал действий, от новых записей к старым.
async function renderJournal() {
  let events;
  try {
    events = await backend().Journal();
  } catch (err) {
    toast(String(err), "error");
    return;
  }
  const box = $("journal");
  box.replaceChildren();
  for (const e of events) {
    const row = el("div", "event");
    row.dataset.kind = e.kind;
    row.append(el("span", "event-time", clock(e.time)), el("span", "event-kind", eventKinds[e.kind] || e.kind), el("span", "event-text", e.text));
    box.append(row);
  }
  $("journal-empty").hidden = events.length > 0;
  $("journal").hidden = events.length === 0;
}

// renderSettings показывает настройки переключателями и подключения
// (игра, Nexus, Vortex) строками с кнопкой.
function renderSettings() {
  const box = $("settings");
  box.replaceChildren();
  for (const item of state.settings || []) {
    const row = el("div", "setting");
    const text = el("div", "setting-text");
    text.append(el("div", "setting-title", item.title), el("div", "setting-detail", item.detail));
    const toggle = el("button", "switch");
    toggle.setAttribute("role", "switch");
    toggle.setAttribute("aria-checked", String(item.on));
    toggle.setAttribute("aria-label", item.title);
    const track = el("span", "switch-track");
    track.append(el("span", "switch-knob"));
    toggle.append(track);
    toggle.addEventListener("click", () => call(() => backend().SetSetting(item.key, !item.on)));
    row.append(text, toggle);
    box.append(row);
  }
  $("settings-empty").hidden = (state.settings || []).length > 0;

  const links = $("connections");
  links.replaceChildren();
  for (const item of state.status) {
    if (!item.command || item.command === "ToggleNxm") continue; // ссылки nxm — переключатель выше
    const row = el("div", "setting");
    const text = el("div", "setting-text");
    text.append(el("div", "setting-title", item.label), el("div", "setting-detail", item.value));
    const button = el("button", "ghost", item.command === "Release" ? "Вернуть Vortex" : "Изменить");
    button.addEventListener("click", () => run(item.command));
    row.append(text, button);
    links.append(row);
  }
  const home = el("div", "setting");
  const homeText = el("div", "setting-text");
  homeText.append(el("div", "setting-title", "Хранилище модов"), el("div", "setting-detail", state.home));
  home.append(homeText);
  links.append(home);
}

// renderSetup показывает памятку «Начало работы», пока не всё сделано.
function renderSetup() {
  const steps = state.setup || [];
  $("setup-section").hidden = steps.length === 0;
  const box = $("setup");
  box.replaceChildren();
  for (const step of steps) {
    const row = el("div", "issue setup-step");
    row.dataset.done = String(step.done);
    const text = el("div", "issue-text");
    text.append(el("div", "issue-title", (step.done ? "✓ " : "○ ") + step.title), el("div", "setup-detail", step.detail));
    row.append(text);
    if (!step.done) {
      const button = el("button", "ghost", step.action);
      button.addEventListener("click", () => run(step.command));
      row.append(button);
    }
    box.append(row);
  }
}

function showTab(tab) {
  for (const button of document.querySelectorAll(".tab")) {
    if (button === tab) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  currentTab = tab.dataset.tab;
  for (const name of ["mods", "downloads", "journal", "settings"]) {
    $("view-" + name).hidden = name !== currentTab;
  }
  hideMenu();
  if (!state) return;
  renderCard();
  if (currentTab === "downloads") renderDownloads();
  if (currentTab === "journal") renderJournal();
  if (currentTab === "settings") renderSettings();
}

// dropFiles принимает архивы, брошенные мышью в окно.
async function dropFiles(paths) {
  if (!paths || !paths.length) return;
  toast(`Добавляю из архивов: ${paths.length}…`, "busy", true);
  try {
    const res = await backend().AddDropped(paths);
    state = res.state;
    render();
    toast(res.message, res.failed ? "error" : "info");
  } catch (err) {
    toast(String(err), "error");
  }
}

function wire() {
  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => showTab(tab));
  }
  for (const button of document.querySelectorAll("button[data-stage]:not(.tab)")) {
    button.addEventListener("click", () => notYet(button.textContent.trim().replace(/:.*/, ""), button.dataset.stage));
  }
  $("search").addEventListener("input", renderMods);

  // Высота закреплённого блока нужна стилям, чтобы названия столбцов
  // прилипали точно под ним.
  const pinned = document.querySelector(".pinned");
  new ResizeObserver(() => {
    $("view-mods").style.setProperty("--pinned-h", `${pinned.offsetHeight}px`);
  }).observe(pinned);

  for (const th of document.querySelectorAll(".mods th[data-sort]")) {
    th.tabIndex = 0;
    th.addEventListener("click", () => sortBy(th.dataset.sort));
    th.addEventListener("keydown", (event) => {
      if (event.key === "Enter") sortBy(th.dataset.sort);
    });
  }
  $("setup-hide").addEventListener("click", () => call(() => backend().HideSetup()));
  $("bisect-start").addEventListener("click", startBisect);
  $("bisect-bad").addEventListener("click", () => bisectStep(() => backend().BisectAnswer(true), $("bisect-bad")));
  $("bisect-good").addEventListener("click", () => bisectStep(() => backend().BisectAnswer(false), $("bisect-good")));
  $("bisect-cancel").addEventListener("click", () => bisectStep(() => backend().CancelBisect(), $("bisect-cancel")));
  // Архив мода можно бросить в окно мышью.
  if (window.runtime && window.runtime.OnFileDrop) window.runtime.OnFileDrop((x, y, paths) => dropFiles(paths), false);

  $("set-button").addEventListener("click", openSets);
  $("picked-new").addEventListener("click", () => newSet([...picked]));
  $("picked-add").addEventListener("click", openAddToSet);
  $("picked-on").addEventListener("click", () => setPickedEnabled(true));
  $("picked-off").addEventListener("click", () => setPickedEnabled(false));
  $("picked-clear").addEventListener("click", clearPicked);
  // Меню закрывается щелчком мимо него и клавишей Esc.
  document.addEventListener("mousedown", (event) => {
    const menu = $("menu");
    if (menu.hidden || menu.contains(event.target)) return;
    const anchor = menu.dataset.anchor ? document.getElementById(menu.dataset.anchor) : null;
    if (!anchor || !anchor.contains(event.target)) hideMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") hideMenu();
  });

  $("add-mod").addEventListener("click", async () => {
    const before = state ? state.mods.map((m) => m.id + m.version).join() : "";
    const wasDemo = state && state.demo;
    if (await call(() => backend().AddMod())) {
      const after = state.mods.map((m) => m.id + m.version).join();
      if (after !== before || wasDemo !== state.demo) toast("Мод добавлен в хранилище");
    }
  });
  $("card-remove").addEventListener("click", () => call(async () => {
    const id = selectedId;
    if (!(await ask(await backend().RemoveAsk(id)))) return state;
    let res = await backend().RemoveMod(id, false);
    if (res.trashUnavailable && (await ask(res.ask))) {
      res = await backend().RemoveMod(id, true);
    }
    return res.state;
  }));
  $("card-show-files").addEventListener("click", toggleFiles);
  $("check-updates").addEventListener("click", () => checkUpdates(false));
  $("card-update").addEventListener("click", () => act(() => backend().UpdateMod(selectedId), $("card-update"), "Обновление: запрос к Nexus…"));
  $("card-nexus").addEventListener("click", async () => {
    try {
      await backend().OpenNexus(selectedId);
    } catch (err) {
      toast(String(err), "error");
    }
  });
  $("deploy").addEventListener("click", deploy);
  $("sort").addEventListener("click", () => run("Sort"));
  $("play").addEventListener("click", async () => {
    try {
      await backend().Play();
      toast("Игра запускается");
    } catch (err) {
      toast(String(err), "error");
    }
  });
  $("plan-files").addEventListener("click", showPlanFiles);
  $("sheet-close").addEventListener("click", () => { $("sheet").hidden = true; });
  $("sheet").addEventListener("click", (event) => {
    if (event.target === $("sheet")) $("sheet").hidden = true;
  });
  $("sheet").addEventListener("keydown", (event) => {
    if (event.key === "Escape") $("sheet").hidden = true;
  });
}

async function start() {
  wire();
  if (!backend()) {
    toast("Страница открыта вне программы: данных нет", "error");
    return;
  }
  try {
    state = await backend().State();
    render();
    listen();
    backend().Ready();
    // Проверка обновлений при запуске идёт в фоне и окну не мешает.
    if (state.checkOnStart) checkUpdates(true);
  } catch (err) {
    toast("Программа не может работать с хранилищем: " + err, "error", true);
  }
}

window.addEventListener("load", start);
