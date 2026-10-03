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
  if (!mod.nexusId) {
    text = "—";
    hint = "У мода нет номера на Nexus: проверить его нельзя";
  } else if (mark === "checking") {
    text = "Проверяется";
    level = "busy";
  } else if (mark === "queued") {
    text = "В очереди";
    level = "queued";
  } else if (mark === "missing") {
    text = "Нет на Nexus";
    hint = "Страница мода на Nexus убрана или скрыта автором";
  } else {
    update = mark && mark !== "ok" ? mark : mod.available;
    if (update) {
      text = update;
      level = "warn";
      hint = "На Nexus есть версия " + update;
    } else if (mark === "ok") {
      text = "✓ Актуален";
      level = "ok";
    }
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

async function checkUpdates() {
  checking = { done: 0, total: 0, name: "" };
  marks.clear();
  for (const mod of state.mods) if (mod.nexusId) marks.set(mod.id, "queued");
  renderCheck();
  renderMods();
  try {
    await act(() => backend().CheckUpdates(), $("check-updates"));
  } finally {
    checking = null;
    // Найденную версию дальше показывает само состояние мода; до кого
    // проверка не дошла (сбой), у того отметка снимается.
    for (const [id, mark] of marks) {
      if (mark === "queued" || mark === "checking") marks.delete(id);
      else if (mark !== "missing") marks.set(id, "ok");
    }
    renderCheck();
    renderMods();
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

// Всплывающее меню под кнопкой. Пункт: {label, hint, current, disabled,
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
  const box = anchor.getBoundingClientRect();
  menu.style.top = `${box.bottom + 4}px`;
  menu.style.left = `${Math.max(8, Math.min(box.left, window.innerWidth - menu.offsetWidth - 8))}px`;
  menu.dataset.anchor = anchor.id;
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

function renderMods() {
  const body = $("mods");
  const query = $("search").value.trim().toLowerCase();
  body.replaceChildren();

  state.mods.forEach((mod, index) => {
    if (query && !mod.name.toLowerCase().includes(query)) return;

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

    const nameCell = el("td", "name", mod.name);
    nameCell.title = mod.name;
    const versionCell = el("td", "version", mod.version);
    versionCell.title = mod.version;
    const updateTd = el("td", "update");
    updateCell(updateTd, mod);

    row.append(el("td", "num", String(index + 1)), toggleCell, nameCell, versionCell, updateTd, stateCell);

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
    body.append(row);
  });

  $("mods-empty").hidden = body.children.length > 0;
}

function renderCard() {
  const mod = state.mods.find((m) => m.id === selectedId);
  const card = $("card");
  card.hidden = !mod || !$("view-placeholder").hidden;
  if (!mod) return;

  $("card-name").textContent = mod.name;
  $("card-source").textContent = mod.source;
  $("card-version").textContent = mod.version;
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
    const refuse = question.input ? null : false;
    $("ask-field").hidden = !question.input;
    $("ask-input-label").textContent = question.placeholder || "";
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
  NexusKey: async () => {
    const key = await ask(await backend().NexusKeyAsk());
    return key === null ? state : backend().NexusLogin(key);
  },
  ToggleNxm: () => confirmThen(() => backend().NxmAsk(), () => backend().ToggleNxm()),
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

function showTab(tab) {
  for (const button of document.querySelectorAll(".tab")) {
    if (button === tab) button.setAttribute("aria-current", "page");
    else button.removeAttribute("aria-current");
  }
  const isMods = tab.dataset.tab === "mods";
  $("view-mods").hidden = !isMods;
  $("view-placeholder").hidden = isMods;
  if (!isMods) {
    $("placeholder-title").textContent = tab.textContent;
    $("placeholder-text").textContent = `Раздел «${tab.textContent}» появится на этапе ${tab.dataset.stage}.`;
  }
  if (state) renderCard();
}

function wire() {
  for (const tab of document.querySelectorAll(".tab")) {
    tab.addEventListener("click", () => showTab(tab));
  }
  for (const button of document.querySelectorAll("button[data-stage]:not(.tab)")) {
    button.addEventListener("click", () => notYet(button.textContent.trim().replace(/:.*/, ""), button.dataset.stage));
  }
  $("search").addEventListener("input", renderMods);

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
    const anchor = document.getElementById(menu.dataset.anchor);
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
  $("check-updates").addEventListener("click", checkUpdates);
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
  } catch (err) {
    toast("Программа не может работать с хранилищем: " + err, "error", true);
  }
}

window.addEventListener("load", start);
