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

// Значки списка модов: контуры 16×16 цветом текста.
const ICONS = {
  nexus: '<path d="M8 1.5 14 5v6l-6 3.5L2 11V5z"/>',
  disk: '<rect x="2" y="2.5" width="12" height="3.5"/><path d="M3 6v7.5h10V6M6.5 9h3"/>',
  link: '<path d="M6.5 9.5l3-3M7.5 4.5l1-1a2.8 2.8 0 0 1 4 4l-1 1M8.5 11.5l-1 1a2.8 2.8 0 0 1-4-4l1-1"/>',
  manual: '<path d="M1.5 3.5h5l1.5 1.5h6.5v8h-13z"/>',
  requires: '<path d="M1.5 8h8M6.5 5l3 3-3 3M13 2.5v11"/>',
  needed: '<path d="M14.5 8h-8M9.5 5l-3 3 3 3M3 2.5v11"/>',
  conflict: '<path d="M2.5 2.5l11 11M13.5 2.5l-11 11M2.5 10.5l3 3M10.5 13.5l3-3"/>',
};

// icon — значок из ICONS с подсказкой.
function icon(name, title) {
  const node = el("span", "ico ico-" + name);
  node.innerHTML = `<svg viewBox="0 0 16 16" aria-hidden="true">${ICONS[name]}</svg>`;
  if (title) node.title = title;
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
  toast(t`«${label}» появится на этапе ${stage}`);
}

function renderStatus() {
  const box = $("status");
  box.replaceChildren();
  for (const item of state.status) {
    if (item.quiet) continue; // всё в порядке — пункт есть только в «Настройках»
    const node = el("span", "status-item");
    node.dataset.level = item.level;
    let value = el("span", "status-value", item.value);
    value.title = item.value;
    if (item.command) {
      value = el("button", "status-value status-command", item.value);
      value.title = item.value + t(" — щёлкните, чтобы открыть подробности");
      value.addEventListener("click", () => run(item.command));
    }
    node.append(el("span", "muted", item.label), value);
    box.append(node);
  }
}

// Команды, которые сами только показывают подробности и ничего не меняют.
const explainCommands = ["ShowFiles", "ChooseWinner", "ShowConflicts"];

// explainIssue показывает пояснение к замечанию: подробный разбор, если он
// есть, иначе само замечание целиком — с его действием на выбор.
async function explainIssue(issue) {
  if (explainCommands.includes(issue.command)) return run(issue.command, issue.arg);
  const answer = await ask({ title: issue.title, message: issue.detail, ok: issue.command ? issue.action : "" });
  if (answer === true && issue.command) run(issue.command, issue.arg);
}

function renderIssues() {
  const box = $("issues");
  box.replaceChildren();
  for (const issue of state.issues) {
    const row = el("div", "issue");
    row.dataset.level = issue.level;
    const text = el("div", "issue-text");
    text.append(el("div", "issue-title", issue.title), el("div", "issue-detail", issue.detail));
    text.title = t("Щёлкните, чтобы открыть пояснение");
    text.addEventListener("click", () => explainIssue(issue));
    row.append(text);
    if (issue.action) {
      const button = el("button", "ghost", issue.action);
      button.addEventListener("click", () => (issue.command ? run(issue.command, issue.arg) : notYet(issue.action, issue.stage)));
      row.append(button);
    }
    if (issue.key) {
      // Замечание, с которым решено жить, можно убрать с глаз.
      const hide = el("button", "ghost small-button", t("Скрыть"));
      hide.title = t("Убрать это замечание из списка. Вернуть скрытые можно ссылкой под списком");
      hide.addEventListener("click", () => call(() => backend().HideIssue(issue.key)));
      row.append(hide);
    }
    box.append(row);
  }
  const hidden = state.hiddenIssues || 0;
  $("issues").hidden = state.issues.length === 0;
  $("issues-hidden").hidden = hidden === 0;
  $("issues-hidden-count").textContent = t`Скрыто замечаний: ${hidden}`;
  $("issues-section").hidden = state.issues.length === 0 && hidden === 0;
}
