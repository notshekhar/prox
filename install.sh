#!/usr/bin/env bash
# prox installer: prebuilt binary from GitHub Releases, no runtime required.
#   curl -fsSL https://raw.githubusercontent.com/notshekhar/prox/main/install.sh | bash
#
# From a checkout, build it here instead:
#   ./install.sh --from-source
#
# Layout after install:
#   ~/.prox/                     config, keys and account logins
#   ~/.prox/bin/
#     ├── prox                   the executable
#     └── .version               the tag it came from
#   $BIN_DIR/prox → ~/.prox/bin/prox
#
# Flags (curl | bash -s -- <flags>), each with the env knob beside it:
#   -v, --version <vX.Y.Z>   pin a specific tag        (PROX_VERSION)
#       --force              skip the up-to-date gate  (PROX_FORCE=1)
#       --from-source        build with go and bun     (PROX_FROM_SOURCE=1)
#       --uninstall          remove binary and links   (PROX_UNINSTALL=1)
#       --no-modify-path     don't touch the shell rc  (PROX_NO_MODIFY_PATH=1)
#   -h, --help
#
# Extra env knobs:
#   PROX_REPO_SLUG      notshekhar/prox   override the repo
#   PROX_DOWNLOAD_BASE                    override the release download URL
#   PROX_BIN_HOME       ~/.prox/bin       install dir
#   PROX_BIN_DIR                          symlink dir (auto: /usr/local/bin, else
#                                         /opt/homebrew/bin, else ~/.local/bin)

set -euo pipefail

REPO_SLUG="${PROX_REPO_SLUG:-notshekhar/prox}"
REPO="${PROX_REPO:-https://github.com/${REPO_SLUG}.git}"
REF="${PROX_REF:-main}"
PROX_BIN_HOME="${PROX_BIN_HOME:-${PROX_HOME:-$HOME/.prox}/bin}"
FORCE="${PROX_FORCE:-0}"
FROM_SOURCE="${PROX_FROM_SOURCE:-0}"
UNINSTALL="${PROX_UNINSTALL:-0}"
PIN_VERSION="${PROX_VERSION:-}"
NO_MODIFY_PATH="${PROX_NO_MODIFY_PATH:-0}"

usage() {
  cat <<EOF
prox installer

Usage: install.sh [options]

Options:
  -v, --version <vX.Y.Z>  Install a specific release
      --force             Reinstall even when up to date
      --from-source       Build with go and bun
      --uninstall         Remove the install and its symlinks
      --no-modify-path    Don't write the PATH line to your shell rc
  -h, --help              Show this help

Examples:
  curl -fsSL https://raw.githubusercontent.com/${REPO_SLUG}/main/install.sh | bash
  curl -fsSL https://raw.githubusercontent.com/${REPO_SLUG}/main/install.sh | bash -s -- --version v0.1.1
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    -v|--version)
      if [ -n "${2:-}" ]; then PIN_VERSION="$2"; shift 2; else
        printf "\033[31m--version requires an argument\033[0m\n" >&2; exit 1; fi ;;
    --force) FORCE=1; shift ;;
    --from-source) FROM_SOURCE=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --no-modify-path) NO_MODIFY_PATH=1; shift ;;
    *) printf "\033[2mignoring unknown option: %s\033[0m\n" "$1" >&2; shift ;;
  esac
done

bold() { printf "\033[1m%s\033[0m\n" "$*"; }
dim()  { printf "\033[2m%s\033[0m\n" "$*"; }
err()  { printf "\033[31m%s\033[0m\n" "$*" >&2; }

need_tool() {
  local cmd="$1" hint="$2"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    err "Missing required tool: $cmd"
    err "  → $hint"
    exit 1
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    err "missing sha256sum/shasum"; return 1
  fi
}

# ver_gt a b → true when a is strictly newer than b.
ver_gt() {
  local a="${1#v}" b="${2#v}"
  [ "$a" = "$b" ] && return 1
  local top
  top="$(printf '%s\n%s\n' "$a" "$b" | sort -V | head -n1)"
  [ "$top" = "$b" ] && return 0
  return 1
}

# ── Download progress bar ──────────────────────────────────────────────────
# curl writes a --trace-ascii stream into a FIFO; the content-length and each
# `<= recv data` record are parsed live to draw a ■■■･･･ 42% bar on stderr.
# Only on a TTY — anything else, or any failure, falls back to plain curl in
# the caller, because a progress bar is never worth failing an install over.
unbuffered_sed() {
  # GNU takes -u, BSD/macOS takes -l; without either, pad each line past the
  # libc buffer so records flush through the pipe as they happen.
  if echo | sed -u -e "" >/dev/null 2>&1; then
    sed -nu "$@"
  elif echo | sed -l -e "" >/dev/null 2>&1; then
    sed -nl "$@"
  else
    local pad
    pad="$(printf "\n%512s" "")"
    sed -ne "s/$/\\${pad}/" "$@"
  fi
}

PROGRESS_COLOR='\033[38;5;215m'
PROGRESS_NC='\033[0m'

print_progress() {
  local bytes="$1" length="$2"
  [ "$length" -gt 0 ] || return 0

  local width=50
  local percent=$(( bytes * 100 / length ))
  [ "$percent" -gt 100 ] && percent=100
  local on=$(( percent * width / 100 ))
  local off=$(( width - on ))

  local filled empty
  filled="$(printf "%*s" "$on" "")"; filled="${filled// /■}"
  empty="$(printf "%*s" "$off" "")"; empty="${empty// /･}"

  printf "\r${PROGRESS_COLOR}%s%s %3d%%${PROGRESS_NC}" "$filled" "$empty" "$percent" >&4
}

download_with_progress() {
  local url="$1" output="$2"

  if [ -t 2 ]; then exec 4>&2; else exec 4>/dev/null; fi

  local tmp_dir="${TMPDIR:-/tmp}"
  local tracefile="${tmp_dir}/prox_install_$$.trace"

  rm -f "$tracefile"
  mkfifo "$tracefile" 2>/dev/null || return 1

  # Hide the cursor while the bar redraws; always restore it on the way out.
  printf "\033[?25l" >&4
  trap "trap - RETURN; rm -f \"$tracefile\"; printf '\033[?25h' >&4; exec 4>&-" RETURN

  # -f so an HTTP error fails the download (and the caller's fallback runs)
  # instead of tracing a 404 page into the output file.
  ( curl -f --trace-ascii "$tracefile" -s -L -o "$output" "$url" ) &
  local curl_pid=$!

  unbuffered_sed \
    -e 'y/ACDEGHLNORTV/acdeghlnortv/' \
    -e '/^0000: content-length:/p' \
    -e '/^<= recv data/p' \
    "$tracefile" | \
  {
    local length=0 bytes=0
    while IFS=" " read -r -a line; do
      [ "${#line[@]}" -lt 2 ] && continue
      local tag="${line[0]} ${line[1]}"
      if [ "$tag" = "0000: content-length:" ]; then
        # Each response in a redirect chain restarts the count; the final
        # (asset) response's length is the one the bar ends up tracking.
        length="$(echo "${line[2]}" | tr -d '\r')"
        bytes=0
      elif [ "$tag" = "<= recv" ]; then
        bytes=$(( bytes + ${line[3]} ))
        [ "$length" -gt 0 ] && print_progress "$bytes" "$length"
      fi
    done
  }

  wait $curl_pid
  local ret=$?
  echo "" >&4
  return $ret
}

# ── Detect target ─────────────────────────────────────────────────────────
detect_target() {
  local uname_s uname_m os arch
  uname_s="$(uname -s)"
  uname_m="$(uname -m)"
  case "$uname_s" in
    Darwin) os="darwin" ;;
    Linux)  os="linux" ;;
    MINGW*|MSYS*|CYGWIN*)
      err "Windows is not supported yet. WSL works: install prox inside it."
      exit 1
      ;;
    *)      err "unsupported OS: $uname_s"; exit 1 ;;
  esac
  case "$uname_m" in
    x86_64|amd64)   arch="x64" ;;
    arm64|aarch64)  arch="arm64" ;;
    *)              err "unsupported arch: $uname_m"; exit 1 ;;
  esac
  # A shell under Rosetta reports x86_64 on Apple Silicon — install the
  # native arm64 build rather than the emulated one.
  if [ "$os" = "darwin" ] && [ "$arch" = "x64" ]; then
    if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = "1" ]; then
      arch="arm64"
    fi
  fi
  printf "%s-%s" "$os" "$arch"
}

# ── Resolve the latest release tag ────────────────────────────────────────
# The releases/latest redirect first: it is not subject to the anonymous
# GitHub API rate limit (60/h/IP) that bites CI and shared networks.
resolve_latest_tag() {
  local final tag
  final="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO_SLUG}/releases/latest" 2>/dev/null || true)"
  tag="${final##*/}"
  case "$tag" in
    v[0-9]*) printf "%s" "$tag"; return 0 ;;
  esac
  curl -fsSL "https://api.github.com/repos/${REPO_SLUG}/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(v\{0,1\}[0-9][^"]*\)".*/\1/p' \
    | head -n1 || true
}

resolve_bin_dir() {
  if [ -n "${PROX_BIN_DIR:-}" ]; then
    mkdir -p "$PROX_BIN_DIR"
    printf "%s" "$PROX_BIN_DIR"
    return
  fi
  for d in /usr/local/bin /opt/homebrew/bin; do
    if [ -w "$d" ] 2>/dev/null; then printf "%s" "$d"; return; fi
  done
  local fallback="$HOME/.local/bin"
  mkdir -p "$fallback"
  printf "%s" "$fallback"
}

uninstall() {
  bold "▶ Uninstalling prox"
  if [ -x "$PROX_BIN_HOME/prox" ]; then
    "$PROX_BIN_HOME/prox" service off >/dev/null 2>&1 || true
  fi
  for link in "$HOME/.local/bin/prox" "/usr/local/bin/prox" "/opt/homebrew/bin/prox" \
              "${PROX_BIN_DIR:+$PROX_BIN_DIR/prox}"; do
    [ -n "$link" ] || continue
    if [ -L "$link" ] || [ -f "$link" ]; then
      rm -f "$link" 2>/dev/null && dim "  removed $link" || true
    fi
  done
  rm -rf "$PROX_BIN_HOME" 2>/dev/null && dim "  removed $PROX_BIN_HOME" || true
  bold "✓ Uninstalled."
  dim  "  Your config and accounts in ~/.prox were kept."
  dim  "  Remove them with: rm -rf ~/.prox"
}

# source_checkout prints the checkout this script lives in, if it is run from one.
source_checkout() {
  local dir
  dir="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")" 2>/dev/null && pwd)" || return 1
  [ -f "$dir/go.mod" ] && [ -f "$dir/scripts/build.sh" ] && printf "%s" "$dir"
}

install_from_source() {
  bold "▶ prox installer (source build)"
  need_tool go  "Install Go: https://go.dev/dl/"
  need_tool bun "Install Bun: https://bun.sh"

  rm -rf "${PROX_BIN_HOME}".old.* "${PROX_BIN_HOME}".new.* "${PROX_BIN_HOME}".src.* 2>/dev/null || true
  local checkout scratch="${PROX_BIN_HOME}.src.$$"
  mkdir -p "$(dirname "$PROX_BIN_HOME")"
  trap 'rm -rf "$scratch" 2>/dev/null || true' EXIT
  checkout="$(source_checkout || true)"
  if [ -z "$checkout" ]; then
    need_tool git "Install Git first: https://git-scm.com/downloads"
    bold "▶ Cloning $REPO ($REF)"
    git clone --depth=1 --branch "$REF" "$REPO" "$scratch/repo" 2>/dev/null \
      || git clone --depth=1 "$REPO" "$scratch/repo"
    checkout="$scratch/repo"
  fi

  bold "▶ Building"
  "$checkout/scripts/build.sh"
  mkdir -p "$scratch/stage"
  cp "$checkout/bin/prox" "$scratch/stage/"
  printf "source\n" > "$scratch/stage/.version"
  swap_into_place "$scratch/stage"
  trap - EXIT
  rm -rf "$scratch" 2>/dev/null || true
  link_globally
  smoke_test
  finish_message "from source"
}

install_from_release() {
  bold "▶ prox installer"
  need_tool curl "macOS: preinstalled. Linux: sudo apt install curl"
  need_tool tar  "Standard on macOS/Linux."

  local target latest installed
  target="$(detect_target)"
  dim "  target: $target"

  latest="${PIN_VERSION}"
  [ -z "$latest" ] && latest="$(resolve_latest_tag)"
  if [ -z "$latest" ]; then
    err "could not resolve the latest release tag from $REPO_SLUG"
    err "set PROX_VERSION=vX.Y.Z to pin one, or PROX_FROM_SOURCE=1 to build from source"
    exit 1
  fi
  case "$latest" in v*) ;; *) latest="v$latest" ;; esac

  installed=""
  [ -f "$PROX_BIN_HOME/.version" ] && installed="$(cat "$PROX_BIN_HOME/.version" 2>/dev/null || true)"
  if [ "$FORCE" != "1" ] && [ -n "$installed" ]; then
    if ! ver_gt "${latest#v}" "${installed#v}"; then
      bold "✓ Up to date (installed $installed, latest $latest)"
      dim "  PROX_FORCE=1 to reinstall"
      exit 0
    fi
    dim "  update: $installed → $latest"
  else
    dim "  installing $latest"
  fi

  local scratch tarball sum url base
  # Sweep leftovers from interrupted runs BEFORE the fresh scratch exists, so
  # the glob cannot eat it.
  rm -rf "${PROX_BIN_HOME}".old.* "${PROX_BIN_HOME}".new.* "${PROX_BIN_HOME}".src.* 2>/dev/null || true
  scratch="${PROX_BIN_HOME}.new.$$"
  trap 'rm -rf "$scratch" 2>/dev/null || true' EXIT
  mkdir -p "$scratch"

  base="${PROX_DOWNLOAD_BASE:-https://github.com/${REPO_SLUG}/releases/download/${latest}}"
  url="${base}/prox-${target}.tar.gz"
  tarball="$scratch/prox.tar.gz"
  sum="$scratch/prox.tar.gz.sha256"

  bold "▶ Downloading ${url##*/}"
  if ! { [ -t 2 ] && download_with_progress "$url" "$tarball"; }; then
    if ! curl -fL --progress-bar "$url" -o "$tarball"; then
      err "download failed: $url"
      err "the release may not have a $target asset; PROX_FROM_SOURCE=1 builds it here"
      exit 1
    fi
  fi

  if curl -fsSL "${url}.sha256" -o "$sum" 2>/dev/null && [ -s "$sum" ]; then
    local expected got
    expected="$(awk '{print $1}' "$sum")"
    got="$(sha256_of "$tarball")"
    if [ "$expected" != "$got" ]; then
      err "sha256 mismatch (expected $expected, got $got)"
      exit 1
    fi
    dim "  sha256 ok"
  else
    dim "  sha256 file missing — skipping verify"
  fi

  bold "▶ Extracting"
  tar -xzf "$tarball" -C "$scratch"
  if [ ! -x "$scratch/$target/prox" ]; then
    err "the tarball is missing $target/prox"
    exit 1
  fi
  printf "%s\n" "$latest" > "$scratch/$target/.version"

  # Gatekeeper blocks an unsigned quarantined binary with a scary dialog.
  if [ "$(uname -s)" = "Darwin" ] && command -v xattr >/dev/null 2>&1; then
    xattr -dr com.apple.quarantine "$scratch/$target" 2>/dev/null || true
  fi

  swap_into_place "$scratch/$target"
  trap - EXIT
  rm -rf "$scratch" 2>/dev/null || true

  link_globally
  smoke_test
  finish_message "$latest"
}

# swap_into_place moves the staged directory over the old install.
#
# A move rather than a copy-over: the old install is set aside first and
# deleted only once the new one is in place, so an interrupted install leaves
# a working prox rather than half of two.
swap_into_place() {
  local src="$1"
  bold "▶ Installing to $PROX_BIN_HOME"
  mkdir -p "$(dirname "$PROX_BIN_HOME")"
  local backup=""
  if [ -e "$PROX_BIN_HOME" ]; then
    backup="${PROX_BIN_HOME}.old.$$"
    mv "$PROX_BIN_HOME" "$backup"
  fi
  mv "$src" "$PROX_BIN_HOME"
  [ -n "$backup" ] && rm -rf "$backup" 2>/dev/null || true
}

link_globally() {
  bold "▶ Linking prox"
  # Clear shims from a previous install style before symlinking, or an old
  # copy earlier on PATH keeps winning and the update looks like it did
  # nothing.
  for stale in "$HOME/.local/bin/prox" "/usr/local/bin/prox" "/opt/homebrew/bin/prox"; do
    if [ -L "$stale" ] || [ -f "$stale" ]; then rm -f "$stale" 2>/dev/null || true; fi
  done

  local bin_dir
  bin_dir="$(resolve_bin_dir)"
  ln -sf "$PROX_BIN_HOME/prox" "$bin_dir/prox"
  hash -r 2>/dev/null || true

  case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *) modify_path "$bin_dir" ;;
  esac

  # GitHub Actions: expose the bin dir to later workflow steps.
  if [ "${GITHUB_ACTIONS:-}" = "true" ] && [ -n "${GITHUB_PATH:-}" ]; then
    echo "$bin_dir" >> "$GITHUB_PATH"
    dim "  added $bin_dir to \$GITHUB_PATH"
  fi

  PROX_LINK_DIR="$bin_dir"
}

modify_path() {
  local bin_dir="$1" shell_name line config_file=""
  shell_name="$(basename "${SHELL:-bash}")"
  local xdg="${XDG_CONFIG_HOME:-$HOME/.config}"
  local candidates
  case "$shell_name" in
    fish) candidates="$HOME/.config/fish/config.fish"
          line="fish_add_path $bin_dir" ;;
    zsh)  candidates="${ZDOTDIR:-$HOME}/.zshrc ${ZDOTDIR:-$HOME}/.zshenv $xdg/zsh/.zshrc"
          line="export PATH=\"$bin_dir:\$PATH\"" ;;
    bash) candidates="$HOME/.bashrc $HOME/.bash_profile $HOME/.profile $xdg/bash/.bashrc"
          line="export PATH=\"$bin_dir:\$PATH\"" ;;
    *)    candidates="$HOME/.profile"
          line="export PATH=\"$bin_dir:\$PATH\"" ;;
  esac

  if [ "$NO_MODIFY_PATH" = "1" ]; then
    err "warning: $bin_dir is not on PATH (--no-modify-path given)"
    err "  $line"
    return 0
  fi

  for f in $candidates; do
    if [ -f "$f" ]; then config_file="$f"; break; fi
  done
  if [ -z "$config_file" ] || [ ! -w "$config_file" ]; then
    err "warning: $bin_dir is not on PATH — add it to your shell rc:"
    err "  $line"
    return 0
  fi
  if grep -Fxq "$line" "$config_file" 2>/dev/null; then
    dim "  PATH line already in $config_file"
    return 0
  fi
  printf "\n# prox\n%s\n" "$line" >> "$config_file"
  dim "  added $bin_dir to PATH in $config_file (restart your shell to pick it up)"
}

# The binary must actually run here — this catches a libc or architecture
# surprise now rather than on first use.
smoke_test() {
  if ! "$PROX_BIN_HOME/prox" --help >/dev/null 2>&1; then
    err "the installed binary did not run"
    err "  try PROX_FROM_SOURCE=1 to build for this machine"
    exit 1
  fi
  dim "  verified: it runs"
}

finish_message() {
  local label="$1"
  bold "✓ Installed $label"
  echo "  prox:    $PROX_LINK_DIR/prox"
  echo "  files:   $PROX_BIN_HOME"
  echo
  echo "To start:"
  echo
  printf "  prox login claude   "; dim "# add an account, once per account"
  printf "  prox start          "; dim "# run the proxy"
  printf "  prox claude         "; dim "# open Claude Code through it"
  printf "  prox dashboard      "; dim "# see your accounts"
  echo
  dim "Docs: https://github.com/${REPO_SLUG}#readme"
}

# ── Route ──────────────────────────────────────────────────────────────────
if [ "$UNINSTALL" = "1" ]; then
  uninstall
elif [ "$FROM_SOURCE" = "1" ]; then
  install_from_source
else
  install_from_release
fi
