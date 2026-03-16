# PEN Installer for Windows — https://github.com/edbnme/pen
# Usage: irm https://raw.githubusercontent.com/edbnme/pen/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$Repo = "edbnme/pen"
$Binary = "pen.exe"

# ── Banner ───────────────────────────────────────────────────────────────────
function Write-Color($Text, $Color) { Write-Host $Text -ForegroundColor $Color -NoNewline }
function Write-ColorLine($Text, $Color) { Write-Host $Text -ForegroundColor $Color }

Write-Host ""
Write-ColorLine @"
  ██████╗ ███████╗███╗   ██╗
  ██╔══██╗██╔════╝████╗  ██║
  ██████╔╝█████╗  ██╔██╗ ██║
  ██╔═══╝ ██╔══╝  ██║╚██╗██║
  ██║     ███████╗██║ ╚████║
  ╚═╝     ╚══════╝╚═╝  ╚═══╝
"@ Cyan

Write-Host ""
Write-Host "  AI-Powered Browser Performance Engineering" -ForegroundColor White
Write-Host ""
Write-ColorLine "  ────────────────────────────────────────────────────" DarkGray
Write-Host ""

# ── Detect platform ─────────────────────────────────────────────────────────
$Arch = $env:PROCESSOR_ARCHITECTURE
switch ($Arch) {
    "AMD64" { $GoArch = "amd64" }
    "x86" { $GoArch = "amd64" }  # 32-bit OS on 64-bit CPU
    "ARM64" {
        Write-Color "  ✗ " Red
        Write-Host "Windows ARM64 is not supported yet."
        exit 1
    }
    default {
        Write-Color "  ✗ " Red
        Write-Host "Unsupported architecture: $Arch"
        exit 1
    }
}

Write-Color "  ✓ " Green
Write-Host "Platform: windows/$GoArch"

# ── Fetch latest version ────────────────────────────────────────────────────
Write-Color "  " DarkGray
Write-Color "Fetching latest version... " DarkGray

try {
    $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    $Version = $Release.tag_name -replace '^v', ''
}
catch {
    Write-Color "✗ " Red
    Write-Host "Could not fetch latest version. Check https://github.com/$Repo/releases"
    exit 1
}

if (-not $Version) {
    Write-Color "✗ " Red
    Write-Host "Could not determine latest version."
    exit 1
}

Write-ColorLine "v$Version" Green

# ── Download ─────────────────────────────────────────────────────────────────
$Filename = "pen_${Version}_windows_${GoArch}.zip"
$Url = "https://github.com/$Repo/releases/download/v${Version}/$Filename"

Write-Color "  " DarkGray
Write-Color "Downloading " DarkGray
Write-Color "$Filename" Cyan
Write-Color "... " DarkGray

$TmpDir = Join-Path $env:TEMP "pen-install-$(Get-Random)"
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

try {
    $ZipPath = Join-Path $TmpDir $Filename
    Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing
    Write-ColorLine "done" Green
}
catch {
    Write-Color "✗ " Red
    Write-Host "Download failed. Check https://github.com/$Repo/releases/tag/v$Version"
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
    exit 1
}

# ── Verify checksum ─────────────────────────────────────────────────────────
Write-Color "  " DarkGray
Write-Color "Verifying checksum... " DarkGray

$ChecksumOk = $false
$ChecksumUrl = "https://github.com/$Repo/releases/download/v${Version}/checksums.txt"

try {
    $ChecksumsPath = Join-Path $TmpDir "checksums.txt"
    Invoke-WebRequest -Uri $ChecksumUrl -OutFile $ChecksumsPath -UseBasicParsing

    $Expected = (Get-Content $ChecksumsPath | Where-Object { $_ -match $Filename } | ForEach-Object { ($_ -split '\s+')[0] })
    $Actual = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLower()

    if ($Expected -and ($Actual -eq $Expected)) {
        $ChecksumOk = $true
        Write-ColorLine "verified" Green
    }
    elseif ($Expected) {
        Write-Color "✗ " Red
        Write-Host "Checksum mismatch!"
        Write-Host "  Expected: $Expected"
        Write-Host "  Got:      $Actual"
        Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
        exit 1
    }
}
catch {
    # Checksum file not available
}

if (-not $ChecksumOk) {
    Write-ColorLine "skipped" Yellow
}

# ── Install ──────────────────────────────────────────────────────────────────
$InstallDir = Join-Path $env:LOCALAPPDATA "pen"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

Write-Color "  " DarkGray
Write-Color "Installing to " DarkGray
Write-Color "$InstallDir" Cyan
Write-Color "... " DarkGray

try {
    Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force
    $ExtractedBinary = Join-Path $TmpDir $Binary
    if (-not (Test-Path $ExtractedBinary)) {
        # Try looking in subdirectories
        $ExtractedBinary = Get-ChildItem -Path $TmpDir -Filter $Binary -Recurse | Select-Object -First 1 -ExpandProperty FullName
    }
    Copy-Item -Path $ExtractedBinary -Destination (Join-Path $InstallDir $Binary) -Force
    Write-ColorLine "done" Green
}
catch {
    Write-Color "✗ " Red
    Write-Host "Installation failed: $_"
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
    exit 1
}

# Cleanup
Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue

Write-Host ""
Write-ColorLine "  ────────────────────────────────────────────────────" DarkGray
Write-Host ""

# ── Add to PATH ──────────────────────────────────────────────────────────────
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Color "  " DarkGray
    Write-Color "Adding to PATH... " DarkGray
    try {
        [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
        $env:Path = "$env:Path;$InstallDir"
        Write-ColorLine "done" Green
    }
    catch {
        Write-ColorLine "failed" Yellow
        Write-Host ""
        Write-Color "  ! " Yellow
        Write-Host "Add $InstallDir to your PATH manually."
    }
}
else {
    Write-Color "  ✓ " Green
    Write-Host "Already in PATH"
}

# ── Verify ───────────────────────────────────────────────────────────────────
$PenPath = Join-Path $InstallDir $Binary
try {
    $InstalledVersion = & $PenPath --version 2>&1
    Write-Color "  ✓ " Green
    Write-Host "Installed: $InstalledVersion"
}
catch {
    Write-Color "  ✓ " Green
    Write-Host "Installed pen to $InstallDir"
}

Write-Host ""

# ── Next step ────────────────────────────────────────────────────────────────
Write-Host "  Run " -NoNewline
Write-Color "pen init" Cyan
Write-Host " to set up your IDE and browser."
Write-Host ""
