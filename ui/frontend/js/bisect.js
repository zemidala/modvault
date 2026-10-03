// Поиск сбойного мода делением пополам: программа включает часть модов,
// пользователь запускает игру и отвечает, осталась ли проблема.
function renderBisect() {
  const b = state.bisect;
  $("bisect-section").hidden = !b;
  $("bisect-start").hidden = !!b || state.demo;
  if (!b) return;
  const found = b.found.map((name) => `«${name}»`).join(", ");
  let title = t`Шаг ${b.step}: `;
  let detail = t("Запустите игру и посмотрите, повторяется ли проблема. Потом ответьте здесь. ");
  if (b.kind === "base" && !b.found.length) {
    title += t("моды набора выключены");
    detail += t("В игре только загрузчик модов и фреймворк: если проблема осталась и так, дело не в модах.");
  } else if (b.kind === "base") {
    title += t`контрольный — включены только ${found}`;
    detail += b.found.length > 1
      ? t("Если проблема повторится — её вызывают эти моды вместе.")
      : t("Если проблема повторится — виновник он. Если нет — её вызывают несколько модов вместе, и поиск продолжится.");
  } else if (b.kind === "verify") {
    title += t("включены все моды набора");
    detail += t("С частью модов проблема ни разу не повторилась: программа проверяет, что с полным набором она есть.");
  } else {
    title = t`Шаг ${b.step} из примерно ${b.steps}: под подозрением ${b.suspects}, сейчас включено ${b.testing.length}`;
    detail += t("Программа сузит круг.") + (b.found.length ? t` Уже найден ${found} — он включён во всех шагах; ищется, с кем вместе он вызывает проблему.` : "") +
      t(" Включены: ") + b.testing.join(", ") + ".";
  }
  $("bisect-title").textContent = title;
  $("bisect-detail").textContent = detail;
  // Подсказка по журналу игры: что было в запуске после этого шага.
  const hint = $("bisect-hint");
  hint.textContent = b.hint + (b.suggest === "problem" ? t(" — похоже, проблема осталась.") : b.suggest === "ok" ? t(" — похоже, проблемы нет.") : ".");
  hint.className = "bisect-hint" + (b.crashed ? " bad" : b.ran ? " ran" : "");
  $("bisect-bad").classList.toggle("suggested", b.suggest === "problem");
  $("bisect-good").classList.toggle("suggested", b.suggest === "ok");
}

// Пока идёт поиск, окно следит за журналом игры: вернулись из игры —
// подсказка уже на месте.
async function refreshBisect() {
  if (!state || !state.bisect || document.hidden || busyBisect) return;
  try {
    const fresh = await backend().BisectStatus();
    if (busyBisect || !state.bisect || !fresh || JSON.stringify(fresh) === JSON.stringify(state.bisect)) return;
    state.bisect = fresh;
    renderBisect();
  } catch (err) { /* следующая попытка — при следующем возврате в окно */ }
}
let busyBisect = false;

async function bisectStep(request, button) {
  button.disabled = true;
  button.classList.add("busy");
  busyBisect = true;
  toast(t("Поиск сбойного мода: игра приводится к следующему шагу…"), "busy", true);
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
    busyBisect = false;
  }
  // Поиск закончен без виновника: итог не должен мелькнуть и пропасть.
  if (res && res.done && !res.culprits.length) {
    await ask({ title: t("Поиск сбойного мода закончен"), message: res.message });
  }
  // Найдено сочетание модов: проблема уходит без любого из них — какой
  // выключить, решает пользователь.
  if (res && res.done && res.culprits.length > 1) {
    const id = await ask({
      title: t("Найдено сочетание модов"),
      message: t`Проблему вызывают вместе: ${res.culprits.map((c) => `«${c.name}»`).join(", ")}. По отдельности они её не дают.\n\nЧтобы проблема ушла, достаточно выключить любой из них в наборе «${state.profile}».`,
      choices: res.culprits.map((c) => ({ label: t`Выключить «${c.name}»`, value: c.id })),
    });
    if (id) call(() => backend().RemoveFromSet(state.profile, [id]).then((r) => r.state));
  }
  // Виновник найден: предложить сразу выключить его в своём наборе.
  if (res && res.culprit) {
    const yes = await ask({
      title: t("Сбойный мод найден"),
      message: t`Проблему вызывает «${res.culpritName}»: она повторилась, когда в игре был включён только он.\n\nВыключить его в наборе «${state.profile}»? Остальные моды останутся как были.`,
      ok: t("Выключить"),
    });
    if (yes) call(() => backend().RemoveFromSet(state.profile, [res.culprit]).then((r) => r.state));
  }
}

async function startBisect() {
  if (!(await ask(await backend().BisectAsk()))) return;
  bisectStep(() => backend().StartBisect(), $("bisect-start"));
}
