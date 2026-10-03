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
  if (!sticky) toastTimer = setTimeout(() => { node.hidden = true; }, level === "error" ? 8000 : 3500);
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
      button.addEventListener("click", () => (issue.command ? run(issue.command) : notYet(issue.action, issue.stage)));
      row.append(button);
    }
    box.append(row);
  }
  $("issues-section").hidden = state.issues.length === 0;
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

    row.append(el("td", "num", String(index + 1)), toggleCell, el("td", "name", mod.name), el("td", "version", mod.version), stateCell);

    const select = () => {
      selectedId = mod.id === selectedId ? null : mod.id;
      hideFiles();
      renderMods();
      renderCard();
    };
    row.addEventListener("click", select);
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
  $("profile").textContent = state.profile;
  $("version").textContent = state.version;
  $("demo").hidden = !state.demo;
  $("home").hidden = state.demo;
  $("home").textContent = "Хранилище: " + state.home;
  renderStatus();
  renderIssues();
  renderMods();
  renderCard();
  renderPlan();
}

// ask показывает вопрос в окне программы и ждёт ответа: true — действие
// подтверждено. Esc и «Отмена» — отказ.
function ask(question) {
  return new Promise((resolve) => {
    const box = $("ask");
    $("ask-title").textContent = question.title;
    $("ask-message").textContent = question.message;
    const ok = $("ask-ok");
    ok.textContent = question.ok || "Да";
    ok.classList.toggle("danger", !!question.danger);
    const done = (answer) => {
      box.hidden = true;
      box.removeEventListener("keydown", onKey);
      ok.onclick = null;
      $("ask-cancel").onclick = null;
      resolve(answer);
    };
    const onKey = (event) => {
      if (event.key === "Escape") done(false);
    };
    ok.onclick = () => done(true);
    $("ask-cancel").onclick = () => done(false);
    box.addEventListener("keydown", onKey);
    box.hidden = false;
    // Необратимое по умолчанию не выбрано: Enter без раздумий его не запустит.
    (question.danger ? $("ask-cancel") : ok).focus();
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
};

function run(command) {
  if (commands[command]) call(commands[command]);
}

async function deploy() {
  const button = $("deploy");
  button.disabled = true;
  try {
    const res = await backend().Deploy();
    state = res.state;
    render();
    toast(res.message);
  } catch (err) {
    toast(String(err), "error");
  } finally {
    button.disabled = false;
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
  $("deploy").addEventListener("click", deploy);
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
  } catch (err) {
    toast("Программа не может работать с хранилищем: " + err, "error", true);
  }
}

window.addEventListener("load", start);
