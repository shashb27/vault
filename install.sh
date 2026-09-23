#!/usr/bin/env bash
# vault installer for macOS and Linux.
#
#   curl -fsSL https://github.com/shashb27/vault/releases/latest/download/install.sh | bash
#   # or, from a checkout:  ./install.sh
#
# What it does: downloads the vault binary for this machine from the latest GitHub
# release into ~/.local/bin/vault and makes sure ~/.local/bin is on your PATH.
# Nothing else is touched. Uninstall: rm ~/.local/bin/vault
#
#   VAULT_VERSION=v0.3.0-beta.1 ./install.sh     # a specific release
#   VAULT_LOCAL=dist/vault-darwin-arm64 ./install.sh   # install a locally built binary (developers)
set -euo pipefail

REPO="shashb27/vault"
BIN="$HOME/.local/bin"
B=$'\e[1m'; D=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'
ok()   { echo "${G}✓${N} $*"; }
warn() { echo "${Y}!${N} $*"; }
die()  { echo "${R}✗${N} $*" >&2; exit 1; }

os="$(uname -s)"; arch="$(uname -m)"
case "$os" in Darwin) goos=darwin ;; Linux) goos=linux ;; *) die "unsupported OS: $os (Windows: use install.ps1)" ;; esac
case "$arch" in arm64|aarch64) goarch=arm64 ;; x86_64|amd64) goarch=amd64 ;; *) die "unsupported CPU: $arch" ;; esac
asset="vault-${goos}-${goarch}"

mkdir -p "$BIN"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

if [[ -n "${VAULT_LOCAL:-}" ]]; then
  cp "$VAULT_LOCAL" "$tmp/$asset"
else
  if [[ -n "${VAULT_VERSION:-}" ]]; then url="https://github.com/$REPO/releases/download/${VAULT_VERSION}/$asset"
  else url="https://github.com/$REPO/releases/latest/download/$asset"; fi
  echo "  downloading $asset ${VAULT_VERSION:-(latest release)}…"
  if ! curl -fsSL "$url" -o "$tmp/$asset"; then
    # private fork / no anonymous access: fall back to the GitHub CLI if present
    command -v gh >/dev/null || die "download failed from $url"
    tag="${VAULT_VERSION:-$(gh release list -R "$REPO" --limit 1 --json tagName --jq '.[0].tagName' 2>/dev/null || true)}"
    [[ -n "$tag" ]] || die "download failed from $url and gh found no release"
    gh release download "$tag" -R "$REPO" -p "$asset" -D "$tmp" --clobber || die "download failed (curl and gh)."
  fi
fi
chmod +x "$tmp/$asset"
if [[ -e "$BIN/vault" && -L "$BIN/vault" ]]; then rm "$BIN/vault"; fi   # old symlink-style install
mv "$tmp/$asset" "$BIN/vault"
ok "installed $BIN/vault"

# old POC alias in ~/.zshrc would shadow the new command
if grep -qE '^\s*alias vault=' "$HOME/.zshrc" 2>/dev/null; then
  cp "$HOME/.zshrc" "$HOME/.zshrc.vault-backup"
  sed -i.bak -E '/^\s*alias vault=/d' "$HOME/.zshrc" && rm -f "$HOME/.zshrc.bak"
  ok "removed the old 'alias vault=…' line from ~/.zshrc (backup: ~/.zshrc.vault-backup)"
fi

rc="$HOME/.zshrc"; [[ "${SHELL:-}" == */bash ]] && rc="$HOME/.bashrc"
case ":$PATH:" in
  *":$BIN:"*) ok "$BIN is on your PATH" ;;
  *)
    if ! grep -qs 'local/bin' "$rc"; then
      printf '\n# vault\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$rc"
      ok "added ~/.local/bin to PATH in $rc"
    fi
    warn "open a new terminal (or run: source $rc) before using 'vault'" ;;
esac

command -v claude >/dev/null 2>&1 || warn "Claude Code is not installed — install it, run 'claude' once to log in, then come back."

# another program called 'vault' (HashiCorp Vault, an old alias) would answer instead of ours
resolved="$(command -v vault 2>/dev/null || true)"
if [[ -n "$resolved" && "$resolved" != "$BIN/vault" ]]; then
  warn "another 'vault' is first in this shell: $resolved"
  warn "'vault doctor' would say 'unknown command'. Fix one of these ways:"
  warn "  - put ~/.local/bin before it in PATH (edit $rc so the export PATH line comes last), or"
  warn "  - keep both: ln -s $BIN/vault $BIN/cvault   and use 'cvault' for this tool"
fi
for f in "$HOME/.zshrc" "$HOME/.zprofile" "$HOME/.bash_profile" "$HOME/.bashrc" "$HOME/.profile"; do
  if grep -qsE '^\s*alias vault=' "$f"; then
    warn "an old 'alias vault=' line in $f will shadow the new command — delete it and open a new terminal"
  fi
  if grep -qsE '^\s*(function\s+vault\b|vault\s*\(\))' "$f"; then
    warn "a shell FUNCTION named vault is defined in $f (line $(grep -nE '^\s*(function\s+vault\b|vault\s*\(\))' "$f" | head -1 | cut -d: -f1))."
    warn "functions win over PATH, so 'vault doctor' would say 'unknown command'. Delete that block, open a new terminal."
  fi
done

echo
"$BIN/vault" version
echo
echo "${B}Next${N}"
echo "  cd into the shared OneDrive folder your team uses, then:"
echo "    ${B}vault init${N}     if you are the first person there"
echo "    ${B}vault join${N}     if a teammate already made it a vault"
echo "  ${D}Not sure? Run 'vault' anywhere — it tells you where you are and what to do.${N}"
