// Привязка кнопок и запуск страницы.

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
  $("issues-show").addEventListener("click", () => call(() => backend().ShowHiddenIssues()));
  $("columns-button").addEventListener("click", openColumns);
  $("filter-category").addEventListener("click", openCategories);
  $("filter-reset").addEventListener("click", resetFilters);
  $("card-versions-button").addEventListener("click", () => {
    const mod = state.mods.find((m) => m.id === selectedId);
    if (mod) openVersions(mod, $("card-versions-button"));
  });
  applyColumns();
  $("bisect-start").addEventListener("click", startBisect);
  window.addEventListener("focus", refreshBisect);
  setInterval(refreshBisect, 5000);
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
      if (after !== before || wasDemo !== state.demo) toast(t("Мод добавлен в хранилище"));
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
  $("card-update").addEventListener("click", () => act(() => backend().UpdateMod(selectedId), $("card-update"), t("Обновление: запрос к Nexus…")));
  $("card-message").addEventListener("click", () => {
    const mod = state.mods.find((m) => m.id === selectedId);
    if (mod) compose(mod);
  });
  $("compose-cancel").addEventListener("click", closeCompose);
  $("compose-send").addEventListener("click", sendCompose);
  $("compose").addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeCompose();
  });
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
      toast(t("Игра запускается"));
    } catch (err) {
      toast(String(err), "error");
    }
  });
  $("plan-files").addEventListener("click", showPlanFiles);
  $("project-link").addEventListener("click", () => backend().OpenProject());
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
    toast(t("Страница открыта вне программы: данных нет"), "error");
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
    toast(t("Программа не может работать с хранилищем: ") + err, "error", true);
  }
}

window.addEventListener("load", start);
