#!/bin/sh
set -eu

BASE_URL='__BASE_URL__'
targets=''
dry_run=0
mcp_mode=ask
usage() { printf '%s\n' 'Usage: sh install.sh [--url ORIGIN] [--harness codex,claude,opencode] [--mcp|--no-mcp] [--dry-run]' 'Without --harness, choose installed harnesses interactively. MCP setup is also prompted unless specified.'; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --url) [ "$#" -ge 2 ] || { usage; exit 1; }; BASE_URL=$2; shift 2 ;;
    --harness) [ "$#" -ge 2 ] || { usage; exit 1; }; targets=$2; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --mcp) mcp_mode=yes; shift ;;
    --no-mcp) mcp_mode=no; shift ;;
    --help|-h) usage; exit 0 ;;
    *) usage; exit 1 ;;
  esac
done
case "$BASE_URL" in http://*|https://*) ;; *) printf '%s\n' 'Pass --url with the Brainiac HTTP(S) origin.' >&2; exit 1 ;; esac
BASE_URL=${BASE_URL%/}
available=''
for harness in codex claude opencode; do
  if command -v "$harness" >/dev/null 2>&1; then available="$available $harness"; fi
done
if [ -z "$available" ]; then printf '%s\n' 'No installed Codex, Claude Code, or OpenCode command was found in PATH.' >&2; exit 1; fi
printf 'Installed harnesses:%s\n' "$available"
if [ -z "$targets" ]; then
  if ! ( : </dev/tty ) 2>/dev/null; then printf '%s\n' 'Interactive selection requires a terminal; pass --harness explicitly.' >&2; exit 1; fi
  printf '%s' 'Install to which harnesses? Enter names separated by spaces or commas (blank cancels): ' >/dev/tty
  IFS= read -r targets </dev/tty
  [ -n "$targets" ] || { printf '%s\n' 'Cancelled.'; exit 0; }
fi
targets=$(printf '%s' "$targets" | tr ',' ' ')
for harness in $targets; do
  case "$harness" in codex|claude|opencode) ;; *) printf 'Unknown harness: %s\n' "$harness" >&2; exit 1 ;; esac
  case " $available " in *" $harness "*) ;; *) printf 'Harness not installed: %s\n' "$harness" >&2; exit 1 ;; esac
done
if [ "$mcp_mode" = ask ]; then
  if [ "$dry_run" -eq 1 ]; then mcp_mode=yes
  elif ( : </dev/tty ) 2>/dev/null; then
    printf '%s' 'Also configure Brainiac MCP for the selected harnesses? [Y/n]: ' >/dev/tty
    IFS= read -r answer </dev/tty
    case "$answer" in ''|y|Y|yes|Yes) mcp_mode=yes ;; *) mcp_mode=no ;; esac
  else printf '%s\n' 'Pass --mcp or --no-mcp when no terminal is available.' >&2; exit 1; fi
fi
dest_for() {
  case "$1" in
    codex) printf '%s/skills' "${CODEX_HOME:-$HOME/.codex}" ;;
    claude) printf '%s/skills' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}" ;;
    opencode) printf '%s/opencode/skills' "${XDG_CONFIG_HOME:-$HOME/.config}" ;;
  esac
}
if [ "$dry_run" -eq 1 ]; then for harness in $targets; do printf 'Would install both skills to %s\n' "$(dest_for "$harness")"; if [ "$mcp_mode" = yes ]; then printf 'Would configure MCP brainiac for %s at %s/mcp\n' "$harness" "$BASE_URL"; fi; done; exit 0; fi
command -v curl >/dev/null 2>&1 || { printf '%s\n' 'curl is required.' >&2; exit 1; }
command -v tar >/dev/null 2>&1 || { printf '%s\n' 'tar is required.' >&2; exit 1; }
if [ "$mcp_mode" = yes ]; then command -v python3 >/dev/null 2>&1 || { printf '%s\n' 'Python 3 is required for MCP setup.' >&2; exit 1; }; fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
for skill in progress-tracker live-progress-monitor; do
  curl --fail --silent --show-error --connect-timeout 3 --max-time 30 "$BASE_URL/skills/$skill.tar.gz" -o "$tmp/$skill.tar.gz"
  # Only the intended package may be extracted. Reject traversal and symlinks.
  tar -tzf "$tmp/$skill.tar.gz" > "$tmp/entries"
  while IFS= read -r entry; do case "$entry" in "$skill/"*) ;; *) printf '%s\n' 'Unexpected archive path.' >&2; exit 1 ;; esac; case "$entry" in *../*|/*) printf '%s\n' 'Unsafe archive path.' >&2; exit 1 ;; esac; done < "$tmp/entries"
  if tar -tvzf "$tmp/$skill.tar.gz" | awk 'substr($0,1,1)=="l" || substr($0,1,1)=="h" {bad=1} END {exit !bad}'; then printf '%s\n' 'Links in skill archives are not allowed.' >&2; exit 1; fi
  tar -xzf "$tmp/$skill.tar.gz" -C "$tmp"
done
mkdir -p "$tmp/progress-tracker/references"
printf '%s\n' "$BASE_URL" > "$tmp/progress-tracker/references/server-url.txt"
for harness in $targets; do
  dest=$(dest_for "$harness")
  mkdir -p "$dest"
  # Merge only the two named packages; preserve unrelated installed files.
  cp -R "$tmp/progress-tracker" "$tmp/live-progress-monitor" "$dest/"
  printf 'Installed Brainiac skills for %s at %s\n' "$harness" "$dest"
  if [ "$mcp_mode" = yes ]; then
    case "$harness" in
      codex) codex mcp add brainiac --url "$BASE_URL/mcp" ;;
      claude) python3 "$dest/progress-tracker/scripts/configure_mcp.py" claude --url "$BASE_URL" ;;
      opencode) python3 "$dest/progress-tracker/scripts/configure_mcp.py" opencode --url "$BASE_URL" ;;
    esac
  fi
done
printf '%s\n' 'Restart or reload your harness to discover the skills. Python 3 is needed for discovery and the temporary fallback.'
