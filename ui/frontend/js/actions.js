// Перерисовка окна, вопросы пользователю, команды и развёртывание.

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
  $("home").textContent = t("Хранилище: ") + state.home;
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
    $("ask-cancel").textContent = question.ok ? t("Отмена") : t("Закрыть");
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
  ShowFiles: async () => {
    await showFiles();
    return state;
  },
  ShowIssues: async () => {
    showIssues();
    return state;
  },
  OpenStore: async () => {
    await backend().OpenFolder("store");
    return state;
  },
  ShowConflicts: async () => {
    await showConflicts();
    return state;
  },
  CleanLeftovers: async () => {
    const res = await backend().CleanLeftovers();
    toast(res.message);
    return res.state;
  },
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
    toast(t`Загрузка: ${p.name}${share}`, "busy", true);
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
  toast(t("Развёртывание…"), "busy", true);
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

// showFiles объясняет пункт «Файлы в игре»: что с файлами модов не так,
// что это значит и что будет при развёртывании.
async function showFiles() {
  try {
    const rep = await backend().FilesReport();
    $("sheet-title").textContent = rep.title;
    $("sheet-note").textContent = rep.note;
    $("sheet-list").replaceChildren(...rep.lines.map((line) => el("li", "", line)));
    $("sheet-list").hidden = rep.lines.length === 0;
    $("sheet").hidden = false;
    $("sheet-close").focus();
  } catch (err) {
    toast(String(err), "error");
  }
}

// showIssues ведёт к замечаниям: открывает раздел «Моды» и показывает
// блок «Требуют внимания».
function showIssues() {
  showTab(document.querySelector('.tab[data-tab="mods"]'));
  const section = $("issues-section");
  if (section.hidden) {
    toast(t("Замечаний нет: всё в порядке"));
    return;
  }
  section.scrollIntoView({ behavior: "smooth", block: "start" });
  section.classList.remove("flash");
  void section.offsetWidth; // перезапуск подсветки
  section.classList.add("flash");
}

async function showPlanFiles() {
  try {
    const lines = await backend().PlanFiles();
    $("sheet-title").textContent = t("План по файлам");
    $("sheet-note").textContent = t("«+» — файл ляжет в игру, «~» — заменит прежний, «−» — уберётся.");
    $("sheet-list").replaceChildren(...lines.map((line) => el("li", "", line)));
    $("sheet-list").hidden = false;
    $("sheet").hidden = false;
    $("sheet-close").focus();
  } catch (err) {
    toast(String(err), "error");
  }
}

function setEnabled(id, enabled) {
  return call(() => backend().SetEnabled(id, enabled));
}
