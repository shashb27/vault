#!/usr/bin/env bash
# vault installer — puts the `vault` command on your PATH.
#
#   With GitHub CLI (recommended, works for the private repo):
#     gh repo clone shashb27/vault ~/.vault-cli && ~/.vault-cli/install.sh
#
#   Or from any checkout:
#     ./install.sh
#
# What it does: symlinks ~/.local/bin/vault -> <this checkout>/vault.sh and makes
# sure ~/.local/bin is on your PATH. Nothing else is touched. Uninstall:
#     rm ~/.local/bin/vault
set -euo pipefail

HERE="$(cd "$(dirname "$(realpath "$0")")" && pwd)"
BIN="$HOME/.local/bin"
B=$'\e[1m'; D=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'
ok()   { echo "${G}✓${N} $*"; }
warn() { echo "${Y}!${N} $*"; }
die()  { echo "${R}✗${N} $*" >&2; exit 1; }

[[ "$(uname)" == "Darwin" ]] || die "vault is macOS-only for now."
[[ -f "$HERE/vault.sh" ]] || die "vault.sh not found next to this installer."
command -v python3 >/dev/null || die "python3 is required (run: xcode-select --install)."

if [[ "$HERE" == "$HOME/Library/CloudStorage/"* ]]; then
  warn "this checkout lives inside OneDrive. Git and OneDrive fight over .git — better:"
  warn "  gh repo clone shashb27/vault ~/.vault-cli && ~/.vault-cli/install.sh"
fi

mkdir -p "$BIN"
if [[ -e "$BIN/vault" && ! -L "$BIN/vault" ]]; then
  die "$BIN/vault exists and is not a symlink — move it aside first."
fi
ln -sfn "$HERE/vault.sh" "$BIN/vault"
chmod +x "$HERE/vault.sh"
ok "linked $BIN/vault → $HERE/vault.sh"

# old POC alias in ~/.zshrc would shadow the new command
if grep -qE '^\s*alias vault=' "$HOME/.zshrc" 2>/dev/null; then
  cp "$HOME/.zshrc" "$HOME/.zshrc.vault-backup"
  sed -i '' -E '/^\s*alias vault=/d' "$HOME/.zshrc"
  ok "removed the old 'alias vault=…' line from ~/.zshrc (backup: ~/.zshrc.vault-backup)"
fi

case ":$PATH:" in
  *":$BIN:"*) ok "$BIN is on your PATH" ;;
  *)
    if ! grep -qs 'local/bin' "$HOME/.zshrc"; then
      printf '\n# vault\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$HOME/.zshrc"
      ok "added ~/.local/bin to PATH in ~/.zshrc"
    fi
    warn "open a new terminal (or run: source ~/.zshrc) before using 'vault'" ;;
esac

if ! command -v claude >/dev/null 2>&1; then
  warn "Claude Code is not installed — install it, run 'claude' once to log in, then come back."
fi

echo
"$HERE/vault.sh" version
echo
echo "${B}Next${N}"
echo "  cd into the shared OneDrive folder your team uses, then:"
echo "    ${B}vault init${N}     if you are the first person there"
echo "    ${B}vault join${N}     if a teammate already made it a vault"
echo "  ${D}Not sure? Run 'vault' anywhere — it tells you where you are and what to do.${N}"
