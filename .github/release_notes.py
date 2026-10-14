"""Текст выпуска из CHANGELOG.md для релиза на GitHub и для Nexus Mods.

    python .github/release_notes.py check  X.Y.Z    есть ли раздел «## X.Y.Z» с английской и русской частью
    python .github/release_notes.py github X.Y.Z    описание релиза; берёт из окружения SHA256, COMMIT, RUN_URL
    python .github/release_notes.py nexus  X.Y.Z    английские пункты простым текстом — список изменений на Nexus

Для X.Y.Z-rc1 и подобных берётся раздел X.Y.Z.
"""
import os
import re
import sys
from pathlib import Path

CHANGELOG = Path(__file__).resolve().parent.parent / "CHANGELOG.md"
REPO = "zemidala/modvault"


def section(version: str) -> tuple[str, str]:
    """Английская и русская часть раздела версии (без суффикса -rc1 и т.п.)."""
    base = version.split("-", 1)[0]
    text = re.sub(r"<!--.*?-->", "", CHANGELOG.read_text(encoding="utf-8"), flags=re.S)
    parts = re.split(r"^## +(\S+)[^\n]*\n", text, flags=re.M)
    found = dict(zip(parts[1::2], parts[2::2]))
    if base not in found:
        raise SystemExit(f"В CHANGELOG.md нет раздела «## {base}»")
    halves = re.split(r"^---\s*$", found[base].strip(), maxsplit=1, flags=re.M)
    if len(halves) != 2 or not halves[0].strip() or not halves[1].strip():
        raise SystemExit(f"Раздел «## {base}»: нужны английская часть, строка «---» и русская часть")
    return halves[0].strip(), halves[1].strip()


def github(version: str) -> str:
    en, ru = section(version)
    sha = os.environ["SHA256"].upper()
    commit = os.environ["COMMIT"]
    run = os.environ["RUN_URL"]
    return "\n\n".join(
        [
            en,
            "---",
            ru,
            "---",
            "The exe is not code-signed yet: if SmartScreen warns, choose **More info → Run anyway**. / "
            "Программа пока без цифровой подписи: если SmartScreen предупреждает, нажмите "
            "**Подробнее → Выполнить в любом случае**.",
            f"Built by GitHub Actions from [`{commit[:7]}`](https://github.com/{REPO}/commit/{commit}) "
            f"([build log]({run})). Check the file: `gh attestation verify Modvault.exe -R {REPO}`. / "
            f"Собрано GitHub Actions из коммита `{commit[:7]}`; проверить файл: "
            f"`gh attestation verify Modvault.exe -R {REPO}`.",
            f"SHA-256 `Modvault.exe`: `{sha}`",
        ]
    ) + "\n"


def nexus(version: str) -> str:
    """Пункты английской части без разметки Markdown: Nexus показывает список изменений как текст."""
    en, _ = section(version)
    items = []
    for line in en.splitlines():
        if line.startswith("- "):
            line = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", line)
            items.append(line.replace("**", "").replace("`", ""))
    if not items:
        raise SystemExit(f"Раздел «## {version}»: в английской части нет пунктов «- …»")
    return "\n".join(items) + "\n"


def main() -> None:
    if len(sys.argv) != 3 or sys.argv[1] not in ("check", "github", "nexus"):
        raise SystemExit(__doc__)
    cmd, version = sys.argv[1], sys.argv[2]
    if cmd == "check":
        section(version)
        print(f"CHANGELOG.md: раздел {version.split('-', 1)[0]} на месте")
        return
    out = github(version) if cmd == "github" else nexus(version)
    sys.stdout.buffer.write(out.encode("utf-8"))


if __name__ == "__main__":
    main()
