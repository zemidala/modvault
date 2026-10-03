// Язык окна. Исходный язык — русский: строки в скриптах и на странице
// написаны по-русски и служат ключами словаря. Язык задаёт программа
// (lang.js) при запуске; словарь английского — en.js.
//
//   t("строка")            — строка на языке окна
//   t`шаблон ${x} и ${y}`  — то же для строки с подстановками: ключ словаря —
//                            её неизменные части через «{}», а в переводе
//                            подстановки можно переставить: {0}, {1}
//   plural(n, "мод", "мода", "модов") — форма слова по числу

const LANG = window.MODVAULT_LANG === "en" ? "en" : "ru";
const LOCALE = LANG === "en" ? "en-US" : "ru-RU";
const DICT = LANG === "en" ? window.MODVAULT_EN || {} : null;

function t(first, ...values) {
  if (typeof first === "string") {
    return DICT && typeof DICT[first] === "string" ? DICT[first] : first;
  }
  const key = first.join("{}");
  if (DICT && typeof DICT[key] === "string") {
    let next = 0;
    return DICT[key].replace(/\{(\d*)\}/g, (_, index) => String(values[index === "" ? next++ : Number(index)]));
  }
  return first.reduce((text, part, i) => text + String(values[i - 1]) + part);
}

function plural(n, one, few, many) {
  if (DICT) {
    const forms = DICT[`${one}|${few}|${many}`];
    if (typeof forms === "string") return forms.split("|")[n === 1 ? 0 : 1];
  }
  const tens = Math.abs(n) % 100;
  const ones = tens % 10;
  if (tens >= 11 && tens <= 14) return many;
  if (ones === 1) return one;
  return ones >= 2 && ones <= 4 ? few : many;
}

// decimal пишет дробное число так, как принято в языке окна: 3,5 или 3.5.
function decimal(text) {
  return LANG === "en" ? text : text.replace(".", ",");
}

// translatePage переводит то, что написано прямо на странице: текст и
// подсказки. Скрипты подключены в конце страницы, так что она уже собрана.
function translatePage() {
  if (!DICT) return;
  document.documentElement.lang = LANG;
  const swap = (text) => {
    const key = text.trim();
    return key && typeof DICT[key] === "string" ? text.replace(key, DICT[key]) : text;
  };
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (node.parentElement && node.parentElement.tagName !== "SCRIPT") node.nodeValue = swap(node.nodeValue);
  }
  for (const el of document.querySelectorAll("[title], [placeholder], [aria-label]")) {
    for (const name of ["title", "placeholder", "aria-label"]) {
      if (el.hasAttribute(name)) el.setAttribute(name, swap(el.getAttribute(name)));
    }
  }
}

translatePage();
