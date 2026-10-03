// Письмо автору мода: личное сообщение на Nexus от имени владельца ключа.
// Ответ приходит на сайт — прочитать входящие программа не может.

let composeFor = null; // мод, автору которого пишут

// canMessage — автору можно написать: мод с Nexus и ещё есть на нём.
function canMessage(mod) {
  return !!mod.nexusId && mod.updateStatus !== "missing";
}

function compose(mod) {
  composeFor = mod;
  $("compose-title").textContent = "Написать автору";
  $("compose-note").textContent =
    `Личное сообщение на Nexus тому, кто выложил «${mod.name}»${mod.author ? ` (${mod.author})` : ""}, от вашего имени.\n` +
    "Ответ придёт в личные сообщения на сайте: в программе он не появится.";
  $("compose-subject").value = `${mod.name} ${mod.version && mod.version !== "—" ? mod.version : ""}`.trim() + ": ";
  $("compose-body").value = "";
  $("compose").hidden = false;
  $("compose-subject").focus();
}

function closeCompose() {
  $("compose").hidden = true;
  composeFor = null;
}

async function sendCompose() {
  if (!composeFor) return;
  const button = $("compose-send");
  button.disabled = true;
  button.classList.add("busy");
  try {
    const message = await backend().MessageAuthor(composeFor.id, $("compose-subject").value, $("compose-body").value);
    closeCompose();
    toast(message);
  } catch (err) {
    // Окно остаётся открытым: набранный текст не пропадает.
    toast(String(err), "error");
  } finally {
    button.disabled = false;
    button.classList.remove("busy");
  }
}
