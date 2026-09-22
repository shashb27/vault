# vault installer for Windows (PowerShell 5.1 or 7).
#
#   gh auth status        # the repo is private: you need the GitHub CLI logged in
#   gh release download -R shashb27/vault -p install.ps1; .\install.ps1
#   # or, from a checkout:  .\install.ps1
#
# What it does: downloads vault-windows-<arch>.exe from the latest GitHub release
# into %LOCALAPPDATA%\vault\vault.exe and adds that folder to your user PATH.
# Nothing else is touched. Uninstall: remove that folder and the PATH entry.
#
#   $env:VAULT_VERSION = "v0.3.0-beta.1"; .\install.ps1     # a specific release
#   $env:VAULT_LOCAL = "dist\vault-windows-amd64.exe"; .\install.ps1   # locally built binary
$ErrorActionPreference = "Stop"
$Repo = "shashb27/vault"
$Dir  = Join-Path $env:LOCALAPPDATA "vault"
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$Asset = "vault-windows-$Arch.exe"

function Ok($m)   { Write-Host "[ok] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "[!]  $m" -ForegroundColor Yellow }
function Die($m)  { Write-Host "[x]  $m" -ForegroundColor Red; exit 1 }

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("vault-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

if ($env:VAULT_LOCAL) {
  Copy-Item $env:VAULT_LOCAL (Join-Path $tmp $Asset)
} else {
  if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Die "the GitHub CLI (gh) is required because the repo is private: winget install GitHub.cli, then 'gh auth login'" }
  gh auth status *> $null; if ($LASTEXITCODE -ne 0) { Die "run 'gh auth login' first (the repo is private)." }
  # newest release INCLUDING pre-releases (gh's "latest" skips betas)
  $tag = if ($env:VAULT_VERSION) { $env:VAULT_VERSION } else { (gh release list -R $Repo --limit 1 --json tagName --jq '.[0].tagName') }
  if (-not $tag) { Die "no release found at https://github.com/$Repo - do you have access? Ask Shash." }
  Write-Host "  downloading $Asset from $Repo $tag..."
  & gh release download $tag -R $Repo -p $Asset -D $tmp --clobber; if ($LASTEXITCODE -ne 0) { Die "download failed. Do you have access to https://github.com/$Repo ? Ask Shash." }
}

$target = Join-Path $Dir "vault.exe"
if (Test-Path $target) { Move-Item -Force $target "$target.old" }   # a running exe can be renamed, not overwritten
Move-Item -Force (Join-Path $tmp $Asset) $target
Remove-Item -Recurse -Force $tmp
Ok "installed $target"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($userPath -split ";") -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$Dir", "User")
  $env:Path = "$env:Path;$Dir"
  Ok "added $Dir to your user PATH (open a new terminal for other windows to see it)"
} else { Ok "$Dir is on your PATH" }

if (-not (Get-Command claude -ErrorAction SilentlyContinue)) { Warn "Claude Code is not installed - install it, run 'claude' once to log in, then come back." }

Write-Host ""
& $target version
Write-Host ""
Write-Host "Next" -ForegroundColor White
Write-Host "  cd into the shared OneDrive folder your team uses (under $env:OneDrive or $env:OneDriveCommercial), then:"
Write-Host "    vault init     if you are the first person there"
Write-Host "    vault join     if a teammate already made it a vault"
Write-Host "  Not sure? Run 'vault' anywhere - it tells you where you are and what to do."
