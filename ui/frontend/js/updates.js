// Проверка обновлений и столбец «Обновление».

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
    hint = t("У мода нет номера на Nexus: проверить его нельзя");
  } else if (status === "checking") {
    text = t("Проверяется");
    level = "busy";
  } else if (status === "queued") {
    text = t("В очереди");
    level = "queued";
  } else if (status === "missing") {
    text = t("Нет на Nexus");
    hint = t("Страница мода на Nexus убрана или скрыта автором");
  } else if (status === "update") {
    update = mark && mark !== "ok" ? mark : mod.available;
    text = update;
    level = "warn";
    hint = t("На Nexus есть версия ") + update;
  } else if (status === "current") {
    text = t("✓ Актуально");
    level = "ok";
    hint = t("Установлена последняя версия с Nexus");
  } else {
    text = t("Не проверен");
    hint = t("Нажмите «Проверить обновления»");
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
  button.title = t`Обновить «${mod.name}» до ${version}`;
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
    act(() => backend().UpdateMod(mod.id), button, t("Обновление: запрос к Nexus…"));
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
  $("check-label").textContent = checking.name ? t("Проверка обновлений: ") + checking.name : t("Проверка обновлений: запрос к Nexus…");
  $("check-count").textContent = known ? t`${checking.done} из ${checking.total}` : "";
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
    toast((quiet ? t("Проверка обновлений при запуске не удалась: ") : "") + String(err), "error");
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
