#!/usr/bin/env bash
# Релиз с dist/Modvault.exe и описанием notes.md в репозитории $1 — для release.yml.
# Берёт из окружения VERSION, PRERELEASE, GITHUB_REF_NAME, GITHUB_REPOSITORY, GH_TOKEN.
# Если релиз уже есть (перезапуск после сбоя на следующем шаге), не трогает его.
set -euo pipefail

repo="$1"
tag="$GITHUB_REF_NAME"

if gh release view "$tag" -R "$repo" >/dev/null 2>&1; then
  echo "::warning::В $repo релиз $tag уже есть — оставляю как есть"
  exit 0
fi

flags=(--title "Modvault $VERSION" --notes-file notes.md)
if [ "$PRERELEASE" = true ]; then
  flags+=(--prerelease)
else
  flags+=(--latest)
fi
# В репозитории кода тег уже есть; в modvault-releases кода нет — тег ставится на его main.
if [ "$repo" = "$GITHUB_REPOSITORY" ]; then
  flags+=(--verify-tag)
else
  flags+=(--target main)
fi

gh release create "$tag" dist/Modvault.exe -R "$repo" "${flags[@]}"
echo "Релиз: https://github.com/$repo/releases/tag/$tag" | tee -a "$GITHUB_STEP_SUMMARY"
