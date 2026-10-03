// Перестановка модов мышью: мод тянут за номер и бросают выше или ниже
// другого. Выделенные моды переезжают вместе, сохраняя свой порядок.
//
// Сделано на событиях мыши, а не на перетаскивании браузера: оно в окне
// программы отключено, чтобы в окно можно было бросать архивы модов.

let dragging = null; // { ids, target, after }

// inLoadOrder — список стоит в порядке загрузки: только тогда место, куда
// бросили мод, значит то же, что видно на экране.
function inLoadOrder() {
  return !sorting.key || (sorting.key === "num" && !sorting.desc);
}

function dragStart(event, mod) {
  if (event.button !== 0) return;
  event.preventDefault();
  event.stopPropagation();
  if (!inLoadOrder()) {
    toast(t("Переставлять моды можно, когда список стоит в порядке загрузки: щёлкните по столбцу «№»"));
    return;
  }
  // Выделенные моды едут вместе — в том порядке, в каком стоят сейчас.
  const ids = picked.has(mod.id) && picked.size > 1
    ? state.mods.filter((m) => picked.has(m.id)).map((m) => m.id)
    : [mod.id];
  dragging = { ids, target: null, after: false };
  document.body.classList.add("dragging");
  for (const row of $("mods").querySelectorAll("tr")) row.classList.toggle("drag-source", ids.includes(row.dataset.id));
  document.addEventListener("mousemove", dragMove);
  document.addEventListener("mouseup", dragEnd, { once: true });
  document.addEventListener("keydown", dragKey);
}

function dragMarks() {
  for (const row of $("mods").querySelectorAll(".drop-before, .drop-after")) row.classList.remove("drop-before", "drop-after");
}

function dragMove(event) {
  if (!dragging) return;
  dragMarks();
  dragging.target = null;
  // У края окна список сам едет дальше.
  if (event.clientY < 140) window.scrollBy(0, -14);
  else if (event.clientY > window.innerHeight - 90) window.scrollBy(0, 14);
  const under = document.elementFromPoint(event.clientX, event.clientY);
  const row = under && under.closest("#mods tr");
  if (!row || dragging.ids.includes(row.dataset.id)) return;
  const box = row.getBoundingClientRect();
  dragging.after = event.clientY > box.top + box.height / 2;
  dragging.target = row.dataset.id;
  row.classList.add(dragging.after ? "drop-after" : "drop-before");
}

function dragStop() {
  document.removeEventListener("mousemove", dragMove);
  document.removeEventListener("mouseup", dragEnd);
  document.removeEventListener("keydown", dragKey);
  document.body.classList.remove("dragging");
  dragMarks();
  for (const row of $("mods").querySelectorAll(".drag-source")) row.classList.remove("drag-source");
  const done = dragging;
  dragging = null;
  return done;
}

// Esc отменяет перестановку, пока мод не брошен.
function dragKey(event) {
  if (event.key === "Escape") dragStop();
}

async function dragEnd() {
  const done = dragStop();
  if (!done || !done.target) return;
  const moved = await act(() => backend().MoveMods(done.ids, done.target, done.after), $("set-button"));
  if (moved && done.ids.length > 1) clearPicked();
}
