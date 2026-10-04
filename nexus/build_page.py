"""Подставляет в PUBLISH.html тексты для текущей версии: python nexus/build_page.py 0.2.1 <SHA-256 exe>."""
import io
import json
import os
import re
import sys

here = os.path.dirname(os.path.abspath(__file__))
root = os.path.dirname(here)
version = sys.argv[1]
sha = sys.argv[2] if len(sys.argv) > 2 else ""


def read(*parts):
    with io.open(os.path.join(*parts), encoding="utf-8") as f:
        return f.read()


bbcode = read(here, "modvault.bbcode").replace("{{VERSION}}", version)
with io.open(os.path.join(root, "dist", "modvault-%s.bbcode" % version), "w", encoding="utf-8", newline="\n") as f:
    f.write(bbcode)

data = {
    "version": version,
    "sha": sha,
    "url": "https://www.nexusmods.com/games/warhammer40kdarktide/mods?uploadMod=true",
    "name": "Modvault",
    "summary": read(here, "modvault.summary.txt").strip(),
    "bbcode": bbcode,
    "mirrorName": "GitHub",
    "mirrorUrl": "https://github.com/zemidala/modvault-releases/releases/latest",
    "picDir": os.path.join(here, "screenshots", "upload"),
    "zip": os.path.join(root, "dist", "Modvault-%s.zip" % version),
    "fileDesc": "Unpack Modvault.exe into any folder and run it. It is a program, not a mod: do not install it "
    "with Vortex. Not code-signed yet: if SmartScreen warns, choose More info - Run anyway. Windows 10/11, 64-bit.",
    "changelog": "First release on Nexus Mods.",
    "req": "Darktide Mod Loader",
    "permNote": "MIT License: you may use, modify and redistribute Modvault as long as the copyright notice and "
    "the license text are kept. Source code: https://github.com/zemidala/modvault",
    "supportMail": "support@nexusmods.com",
    "mailSubject": "Quarantined file review request - Modvault (Warhammer 40,000: Darktide)",
    "mailBody": "\n".join(
        [
            "Hello,",
            "",
            "Mod page: https://www.nexusmods.com/warhammer40kdarktide/mods/НОМЕР",
            "File: Modvault-%s.zip (contains a single Modvault.exe)" % version,
            "SHA-256 of Modvault.exe: %s" % sha,
            "",
            "The file was quarantined by the automated checks because it contains an executable. Modvault is a "
            "standalone mod manager for Darktide (a Windows program, not a mod), so the exe is the product itself.",
            "",
            "Source code (MIT License): https://github.com/zemidala/modvault",
            "It is written in Go with the Wails framework. Build steps are in the README, section \"Release build\":",
            "",
            "go build -trimpath -tags desktop,production -ldflags \"-H windowsgui -s -w -X "
            "github.com/zemidala/modvault/internal/version.Version=%s\" -o Modvault.exe ./cmd/modvault-gui" % version,
            "",
            "The source of this release is the tag v%s. The same exe is published at "
            "https://github.com/zemidala/modvault-releases/releases/tag/v%s with the same SHA-256." % (version, version),
            "",
            "Could you please review the file and release it from quarantine?",
            "",
            "Thank you,",
            "frinteza78",
        ]
    ),
    "credits": "\n".join(
        [
            "Modvault is written from scratch. Source code (MIT License): https://github.com/zemidala/modvault",
            "It includes open-source components under their own licences: Wails (MIT), bodgit/sevenzip (BSD), "
            "nwaples/rardecode (BSD), golang.org/x/sys (BSD); fonts Forum, PT Sans and Saira Stencil One "
            "(SIL Open Font License 1.1).",
            "The game's bundle database is patched with the Darktide Mod Loader's own dtkit-patch tool, "
            "which is not included in Modvault.",
            "Servo-ModQuisitor by xsSplater and Vortex by Nexus Mods were studied for ideas; "
            "no code from them is used.",
        ]
    ),
}
assert len(data["summary"]) <= 350 and len(data["fileDesc"]) <= 255

block = "/*DATA-BEGIN*/\nconst T = %s;\n/*DATA-END*/" % json.dumps(data, ensure_ascii=False, indent=1).replace("</", "<\\/")
page = os.path.join(here, "PUBLISH.html")
html = read(page)
html, n = re.subn(r"/\*DATA-BEGIN\*/.*?/\*DATA-END\*/", lambda m: block, html, flags=re.S)
assert n == 1
with io.open(page, "w", encoding="utf-8", newline="\n") as f:
    f.write(html)
print("ok", version, len(bbcode))
