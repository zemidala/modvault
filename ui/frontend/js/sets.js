// Выделение модов, всплывающие меню и наборы.

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
      if (!item.keep) hideMenu(); // меню с галочками остаётся открытым
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
  items.push({ separator: true });
  items.push({ label: `Сохранить «${state.profile}» в файл…`, hint: "поделиться", action: () => act(() => backend().ExportSet(), $("set-button")) });
  items.push({ label: "Загрузить набор из файла…", action: importSet });
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
  if (!many && mod.versions > 1) {
    items.push({ separator: true });
    items.push({ label: "Версии…", hint: `в хранилище: ${mod.versions}`, action: () => openVersions(mod, { x, y }) });
  }
  items.push({ separator: true });
  items.push({ label: "В новый набор…", action: () => newSet(ids) });
  showMenu({ x, y }, items);
}
