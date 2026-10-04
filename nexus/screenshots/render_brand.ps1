# Renders the Nexus header and thumbnail with headless Edge and fills upload\ with the files in upload order.
# Usage: powershell -ExecutionPolicy Bypass -File render_brand.ps1
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$out = Join-Path $here 'upload'
New-Item -ItemType Directory -Force $out | Out-Null
$edge = "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe"
if (-not (Test-Path $edge)) { $edge = "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe" }

function Shot($url, $w, $h, $file) {
    $ErrorActionPreference = 'Continue'  # Edge reports progress on stderr
    Remove-Item $file -ErrorAction SilentlyContinue
    & $edge --headless=new --disable-gpu --hide-scrollbars --allow-file-access-from-files `
        --window-size="$w,$h" --virtual-time-budget=8000 "--screenshot=$file" $url 2>$null | Out-Null
    if (-not (Test-Path $file)) { throw "render failed: $file" }
}

$base = 'file:///' + ($here -replace '\\', '/')
Shot "$base/header.html" 1300 372 (Join-Path $out '00_header_1300x372.png')
Shot "$base/thumbnail.html" 1280 720 (Join-Path $out '01_thumbnail_1280x720.png')

# gallery, in the order it is shown on the page
$n = 2
foreach ($name in 'main', 'faulty-mod-search', 'conflict', 'outside-mods', 'downloads', 'settings') {
    Copy-Item (Join-Path $here "$name.png") (Join-Path $out ('{0:d2}_{1}.png' -f $n, $name)) -Force
    $n++
}
Get-ChildItem $out | Select-Object Name, Length
