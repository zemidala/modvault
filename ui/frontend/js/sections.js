// Разбор конфликтов и разделы окна: загрузки, журнал, настройки.

// Разбор конфликтов файлов: список всех конфликтов набора. Щелчок по
// конфликту открывает его разбор с советом и выбором победителя.
async function showConflicts() {
  let list;
  try {
    list = await backend().Conflicts();
  } catch (err) {
    toast(String(err), "error");
    return;
  }
  const kinds = {
    identical: t("безвреден: файлы одинаковые"),
    duplicate: t("два варианта одного мода"),
    covered: t("один мод перекрыт целиком"),
    ordered: t("порядок указал автор"),
    overlap: t("частичное пересечение"),
  };
  $("sheet-title").textContent = t("Конфликты файлов");
  $("sheet-note").textContent = list.length
    ? t("Несколько модов кладут в игру один и тот же файл; остаётся вариант победителя. Щёлкните конфликт, чтобы разобрать его.")
    : t("Конфликтов нет: моды не меняют одни и те же файлы.");
  $("sheet-list").replaceChildren(...list.map((c) => {
    const item = el("li", "conflict-row");
    const winner = c.mods.find((m) => m.winner);
    const state = c.kind === "identical" ? t("Безвреден") : c.pinned ? t("Решён") : t("Ждёт решения");
    const button = el("button", "conflict-open");
    button.dataset.state = c.resolved ? "resolved" : "open";
    button.append(
      el("span", "conflict-state", state),
      el("span", "conflict-mods", c.mods.map((m) => m.name).join(t(" и "))),
      el("span", "conflict-meta", t`файлов: ${c.total} · ${kinds[c.kind] || c.kind} · побеждает ${winner ? winner.name : "—"}`),
    );
    button.addEventListener("click", () => {
      $("sheet").hidden = true;
      run("ChooseWinner", c.key);
    });
    item.append(button);
    return item;
  }));
  $("sheet-list").hidden = list.length === 0;
  $("sheet").hidden = false;
  $("sheet-close").focus();
}

// Разделы окна: моды, загрузки, журнал, настройки.
let currentTab = "mods";
let downloadsTimer = 0;

const eventKinds = {
  deploy: t("Игра"), install: t("Мод"), remove: t("Мод"), set: t("Набор"), nexus: "Nexus", vortex: "Vortex", error: t("Ошибка"),
};

function clock(iso) {
  const d = new Date(iso);
  const two = (n) => String(n).padStart(2, "0");
  return `${two(d.getDate())}.${two(d.getMonth() + 1)} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

// duration — сколько ждать, по-человечески: 40 с, 3 мин, 1 ч 20 мин.
function duration(seconds) {
  const s = Math.max(1, Math.round(seconds));
  if (s < 60) return t`${s} с`;
  if (s < 3600) return t`${Math.round(s / 60)} мин`;
  return t`${Math.floor(s / 3600)} ч ${Math.round((s % 3600) / 60)} мин`;
}

function megabytes(n) {
  return t`${decimal((n / 1048576).toFixed(n < 10485760 ? 1 : 0))} МБ`;
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
    const share = d.total > 0 ? t`${megabytes(d.done)} из ${megabytes(d.total)}` : d.done > 0 ? megabytes(d.done) : "";
    // Скорость и сколько осталось — как только скорость измерена.
    let pace = "";
    if (d.state === "active" && d.speed > 0) {
      pace = t` · ${megabytes(d.speed)}/с`;
      if (d.total > d.done) pace += t` · осталось ${duration((d.total - d.done) / d.speed)}`;
    }
    const status = d.state === "active" ? t`Идёт · ${share}${pace}` : d.state === "done" ? t`Готово · ${clock(d.finished)}` : t`Не удалось · ${clock(d.finished)}`;
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
    const button = el("button", "ghost", item.command === "Release" ? t("Вернуть Vortex") : t("Изменить"));
    button.addEventListener("click", () => run(item.command));
    row.append(text, button);
    links.append(row);
  }
  // Папки, которые можно открыть в Проводнике.
  backend().Folders().then((folders) => {
    const titles = { game: t("Папка игры"), store: t("Хранилище модов"), logs: t("Журналы игры") };
    for (const kind of ["game", "store", "logs"]) {
      if (!folders[kind]) continue;
      const row = el("div", "setting");
      const text = el("div", "setting-text");
      text.append(el("div", "setting-title", titles[kind]), el("div", "setting-detail", folders[kind]));
      const button = el("button", "ghost", t("Открыть"));
      button.addEventListener("click", () => backend().OpenFolder(kind).catch((err) => toast(String(err), "error")));
      row.append(text, button);
      links.append(row);
    }
  }).catch(() => {});
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
  toast(t`Добавляю из архивов: ${paths.length}…`, "busy", true);
  try {
    const res = await backend().AddDropped(paths);
    state = res.state;
    render();
    toast(res.message, res.failed ? "error" : "info");
  } catch (err) {
    toast(String(err), "error");
  }
}
