# Adds `wslc compose ...` to bash/zsh inside a WSL distro.
# Usage: add to ~/.bashrc or ~/.zshrc:
#   source ~/wslc-compose/scripts/wslc-compose.sh
# Other wslc subcommands go straight to wslc.exe.

_wslc_real="$(command -v wslc.exe 2>/dev/null || echo '/mnt/c/Program Files/WSL/wslc.exe')"

wslc() {
  if [ "${1:-}" = "compose" ]; then
    shift
    wslc-compose --wslc "$_wslc_real" "$@"
  else
    "$_wslc_real" "$@"
  fi
}
