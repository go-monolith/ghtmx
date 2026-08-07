#!/usr/bin/env bash
#
# Installs the ghtmx binary from a GitHub release archive, and gopls
# alongside it when a Go toolchain is available. This is a convenience
# wrapper around the release-archive install path documented in
# README.md — the same download, checksum, and extract steps, done for
# you. It is not a third supported path: it is bash-only, so Linux,
# macOS, and WSL. Native Windows follows the manual steps in README.md.
#
# Why both binaries: the editor integrations are thin clients. They
# spawn `ghtmx lsp`, which in turn needs gopls for the embedded Go.
# gopls publishes no prebuilt binaries, so `go install` is the only way
# to get it — and ghtmx does not need it to work, so a missing or broken
# Go toolchain costs you embedded-Go features, not the install.
#
# Where things land. ghtmx is spawned by the editor, so it has to be on
# PATH (or named absolutely in the editor's ghtmx.path); the last thing
# this script does is check and tell you. gopls is found by the server,
# which looks on PATH, then in $HOME/go/bin, then in $HOME/.local/bin
# (cmd/ghtmx/lspcmd/pls FindGopls) — so the defaults below usually land
# somewhere it looks even when that directory never reaches PATH. A
# customized GOBIN or GOPATH breaks that coincidence: put the directory
# on PATH in that case.
#
# Once the binaries are in place, an interactive run offers the VS Code
# extension: it is the one editor whose CLI can install a .vsix without
# the user leaving the terminal, and the extension is useless without the
# binaries this script just installed, so this is the moment to ask. The
# offer is a question, never an assumption — see --no-interactive below.
#
# Usage: install.sh [--no-interactive]
#
#   --no-interactive    ask nothing and read nothing from the terminal.
#                       The VS Code offer is skipped rather than answered
#                       for you — unless GHTMX_INSTALL_VSCODE already
#                       answered it yes, which is a decision and not a
#                       prompt. This is what the VS Code extension passes
#                       when it runs this script (editors/vscode): the
#                       extension is already installed by definition in
#                       that case, and a prompt would be answering a
#                       question nobody can see.
#
# Configured by environment, like every other script in this repo:
#
#   GHTMX_VERSION       tag to install; "v0.1.6" or "0.1.6". Default:
#                       whatever the release "latest" redirect points at.
#   GHTMX_BIN_DIR       install directory. Default: $(go env GOBIN), then
#                       $(go env GOPATH)/bin, then ~/.local/bin. Must be
#                       a real path — the shell expands "~", an env var
#                       does not.
#   GOPLS_VERSION       default v0.23.0 — the version CI and CONTRIBUTING
#                       pin, so it is the one this repo tests against.
#   GHTMX_SKIP_GOPLS    set to any value to install ghtmx only.
#   GHTMX_SKIP_VSCODE   set to any value to never mention VS Code.
#   GHTMX_INSTALL_VSCODE
#                       set to any value to install the VS Code extension
#                       without asking — the assume-yes for unattended
#                       setups (containers, dotfiles) that do want it.
#   GHTMX_OS            override the detected OS. A cross-install skips
#   GHTMX_ARCH          gopls, which would be built for the host.
#   GHTMX_RELEASES_URL  release base URL. The one seam the test server
#                       needs; see internal/installcheck.
#
# Re-running is idempotent: the binary is replaced by a rename inside the
# install directory, so there is no window where it is half written.
#
# On trust. The archive is checked against checksums.txt, but both come
# from the same server over the same connection: that gates corruption
# and truncation, not someone who controls the origin. Fetching this
# script by piping curl into bash verifies nothing beyond TLS to
# raw.githubusercontent.com — the same trust model as `go install
# ...@latest`. Clone the repository and read it if you want more. The
# .vsix gets less than that: checksums.txt is written by the release
# staging step and covers the binary archives only, while the editor
# artifacts are attached afterwards by .github/workflows/editors.yml, so
# there is no digest to compare it against. TLS to github.com is the
# whole of its provenance — which is also true of downloading it from
# the release page by hand, the alternative this replaces.
#
# The whole body is a function called on the last line, so a transfer
# that dies mid-file defines a function and exits rather than running a
# truncated script.
#
# Portability notes. GNU coreutils sha256sum and Perl shasum disagree on
# the -c flags (--ignore-missing in particular), so rather than branch on
# dialect we pull the one expected digest out of checksums.txt with awk
# and compare strings. Same spirit as .github/scripts/release-stage.sh's
# sed/GNU-vs-BSD note. The external commands used here are mirrored by
# shimPath in internal/installcheck/installscript_test.go; adding one
# means adding it there.
set -euo pipefail

fail() {
  echo "install.sh: $*" >&2
  exit 1
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW* | MSYS* | CYGWIN*)
      fail "this script is bash-only and does not cover native Windows. Follow the release-archive steps in README.md, or run it from WSL."
      ;;
    *) fail "unsupported operating system $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) fail "unsupported architecture $(uname -m)" ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
  else
    fail "neither sha256sum nor shasum is installed; cannot verify the download"
  fi
}

# The published extension: editors/vscode/package.json's publisher and
# name, joined the way VS Code identifies an extension. Renaming either
# there would leave this looking for an id nobody publishes, so
# TestInstallScriptExtensionID compares the two.
vscode_extension_id="go-monolith.ghtmx-vscode"

# Asks a yes/no question and answers "no" for anything that is not a
# clear yes, including a question that could not be asked.
#
# Reading from /dev/tty rather than stdin is what makes this work under
# `curl … | bash`, where stdin is the script being read and a `read`
# would eat it. Requiring stdout to be a terminal as well is the check
# that keeps a redirected or captured run — CI, a log file, a test —
# from stopping to ask a question nobody will ever see.
# Both tests are load-bearing, and the stdout one especially: a process
# started from a terminal keeps its controlling terminal even when its
# output is a pipe, so /dev/tty alone is readable inside `go test` and a
# read there would block until someone typed into a window showing none
# of this.
confirm() { # question
  local answer
  [ -t 1 ] || return 1
  [ -r /dev/tty ] || return 1
  printf '%s [y/N] ' "$1" >/dev/tty
  read -r answer </dev/tty || return 1
  case "$answer" in
    y | Y | yes | Yes | YES) return 0 ;;
    *) return 1 ;;
  esac
}

# The VS Code CLI, if this machine has one. `code` on PATH is what every
# platform's install offers; the Applications paths are the fallback for
# a macOS install whose "Shell Command: Install 'code' command in PATH"
# step was never run, which is the common case there.
find_vscode() {
  local candidate resolved
  for candidate in code code-insiders; do
    if resolved=$(command -v "$candidate" 2>/dev/null); then
      echo "$resolved"
      return 0
    fi
  done
  for candidate in \
    "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
    "${HOME:-}/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"; do
    if [ -x "$candidate" ]; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}

# The .vsix attached to a release, by name. It cannot be computed from
# the tag: the file carries the EXTENSION version (0.1.0), and an
# extension version identifies a module series rather than one release,
# so v0.1.5 and v0.1.7 both carry ghtmx-vscode-0.1.0.vsix
# (editors/README.md). GitHub renders a release's asset list into a
# fragment of its own, and that fragment is where the name can be read
# without an API call, a token, or jq. awk rather than grep: the set of
# commands this script shells out to is mirrored by shimPath in
# internal/installcheck/installscript_test.go.
#
# The highest version wins rather than the first one listed: the release
# carries one .vsix today, but a re-upload or a second packaged build
# would put two on the page in whatever order GitHub renders them, and
# picking by position would install whichever landed first. The pattern
# is MAJOR.MINOR.PATCH exactly, which is every extension version the
# policy allows; a prerelease .vsix would need it widened, and until then
# the caller's message is careful not to blame the release for a name
# this cannot read.
vsix_asset_name() { # releases_url tag
  local page
  page="$tmp/assets.html"
  download "$1/expanded_assets/$2" "$page" || return 1
  awk '{
         sub(/\r$/, "")   # a Windows-hosted CLI under WSL sends CRLF
         rest = $0
         # A loop rather than one match(): awk finds the first hit in a
         # line, and nothing says the page puts one asset per line.
         while (match(rest, /ghtmx-vscode-[0-9]+\.[0-9]+\.[0-9]+\.vsix/)) {
           name = substr(rest, RSTART, RLENGTH)
           rest = substr(rest, RSTART + RLENGTH)
           # Drop the fixed "ghtmx-vscode-" (13) and ".vsix" (5).
           split(substr(name, 14, length(name) - 18), part, ".")
           rank = part[1] * 1000000 + part[2] * 1000 + part[3]
           if (rank > best) {
             best = rank
             newest = name
           }
         }
       }
       END { if (newest != "") print newest }' "$page"
}

# Offers the extension, and installs it if the answer is yes. Nothing in
# here may fail the run: ghtmx is installed by the time it is called, so
# every step that can go wrong warns and returns instead.
#
# download() and $fetcher come from main — a bash function definition is
# global once executed, and main runs before this is ever called.
offer_vscode_extension() { # releases_url tag interactive
  local releases_url="$1" tag="$2" interactive="$3"
  local code vsix

  [ -z "${GHTMX_SKIP_VSCODE:-}" ] || return 0
  # No VS Code, nothing to offer, nothing to say: most people running an
  # installer for a command-line tool do not have it and do not care.
  code=$(find_vscode) || return 0

  # Checked before the question so that re-running the script stays the
  # no-op the header promises rather than a prompt every time.
  # sub() because under WSL `code` is often the Windows build, which
  # ends its lines with CRLF; without it the id never matches and the
  # script offers an already-installed extension on every run.
  if "$code" --list-extensions 2>/dev/null |
    awk -v id="$vscode_extension_id" '
      { sub(/\r$/, "") }
      tolower($0) == id { found = 1 }
      END { exit !found }'; then
    echo
    echo "the ghtmx VS Code extension is already installed."
    return 0
  fi

  echo
  if [ -n "${GHTMX_INSTALL_VSCODE:-}" ]; then
    echo "installing the ghtmx VS Code extension (GHTMX_INSTALL_VSCODE is set)"
  elif [ "$interactive" = 0 ]; then
    echo "VS Code is installed but the ghtmx extension is not. This run was"
    echo "  told not to ask (--no-interactive), so nothing was installed. To"
    echo "  add it, re-run this script without that flag, or take"
    echo "  ghtmx-vscode-<version>.vsix from the release page and run:"
    echo
    echo "      code --install-extension ghtmx-vscode-<version>.vsix"
    return 0
  elif ! confirm "Install the ghtmx VS Code extension (syntax highlighting, diagnostics, completion)?"; then
    echo "skipping the VS Code extension. Install it later with this script"
    echo "  or from the release page."
    return 0
  fi

  vsix=$(vsix_asset_name "$releases_url" "$tag") || vsix=""
  if [ -z "$vsix" ]; then
    echo "warning: no ghtmx-vscode-<version>.vsix could be found among release"
    echo "  $tag's assets, so the extension was not installed. Either its"
    echo "  packaging job failed — the extensions ship from v0.1.5 onward —"
    echo "  or it is named in a way this script does not recognize. The"
    echo "  release page will say which:"
    echo
    echo "      $releases_url/tag/$tag"
    return 0
  fi

  if ! download "$releases_url/download/$tag/$vsix" "$tmp/$vsix"; then
    echo "warning: downloading $vsix failed, so the extension was not installed."
    echo "  ghtmx itself is installed and works."
    return 0
  fi
  # --force because the id is already known to be absent: it turns the
  # "already installed" question VS Code would otherwise ask into the
  # answer this script has established, and keeps a stale copy from
  # blocking the install.
  if "$code" --install-extension "$tmp/$vsix" --force; then
    echo "installed the ghtmx VS Code extension ($vsix)"
    echo "  reload VS Code (Developer: Reload Window) to activate it."
  else
    echo "warning: VS Code rejected $vsix, so the extension was not installed."
    echo "  ghtmx itself is installed and works. Retry with:"
    echo
    echo "      $code --install-extension <the .vsix from the release page>"
  fi
}

# Cleanup must never change the script's exit status, so every step here
# is allowed to fail quietly.
tmp=""
staged=""
cleanup() {
  [ -n "$tmp" ] && rm -rf "$tmp" 2>/dev/null || true
  [ -n "$staged" ] && rm -f "$staged" 2>/dev/null || true
  return 0
}

main() {
  local releases_url gopls_version

  # --- arguments ----------------------------------------------------------

  # Everything else is configured by environment, which survives the
  # `curl … | bash` pipe unchanged. The one flag exists because the VS
  # Code extension has to say "do not talk to the terminal" on a command
  # line it shows the user, where an exported variable would not read as
  # part of the command at all.
  local interactive=1
  while [ $# -gt 0 ]; do
    case "$1" in
      --no-interactive) interactive=0 ;;
      *) fail "unknown option $1; the only flag is --no-interactive" ;;
    esac
    shift
  done

  releases_url="${GHTMX_RELEASES_URL:-https://github.com/go-monolith/ghtmx/releases}"
  gopls_version="${GOPLS_VERSION:-v0.23.0}"

  # --- platform -----------------------------------------------------------

  local host_os host_arch os arch
  host_os=$(detect_os)
  host_arch=$(detect_arch)
  os="${GHTMX_OS:-$host_os}"
  arch="${GHTMX_ARCH:-$host_arch}"

  # Overrides go through the same gate as detection: the release matrix
  # in internal/release/release.go says what exists to download.
  case "$os/$arch" in
    linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64) ;;
    *) fail "no release archive is built for $os/$arch" ;;
  esac

  # --- fetching -----------------------------------------------------------

  local fetcher
  if command -v curl >/dev/null 2>&1; then
    fetcher=curl
  elif command -v wget >/dev/null 2>&1; then
    fetcher=wget
  else
    fail "neither curl nor wget is installed"
  fi

  download() { # url dest
    case "$fetcher" in
      curl) curl -fsSL "$1" -o "$2" ;;
      wget) wget -q -O "$2" "$1" ;;
    esac
  }

  # --- version ------------------------------------------------------------

  local tag bare effective
  if [ -n "${GHTMX_VERSION:-}" ]; then
    tag="v${GHTMX_VERSION#v}"
  else
    # The "latest" redirect names the tag in its target URL. Reading it
    # there costs one HEAD request and avoids both a jq dependency and
    # the unauthenticated rate limit api.github.com would impose.
    [ "$fetcher" = curl ] ||
      fail "resolving the latest release needs curl; install curl or set GHTMX_VERSION"
    effective=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases_url/latest") ||
      fail "could not reach $releases_url/latest"
    tag=${effective##*/}
  fi
  # The tag becomes part of a URL and of paths under $tmp, so it is
  # matched exactly rather than merely checked for a leading "v".
  if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.]+)?$ ]]; then
    fail "$tag is not a release version like v1.2.3; set GHTMX_VERSION"
  fi
  bare="${tag#v}"

  # --- install directory --------------------------------------------------

  # Resolved before the download so that an unwritable or unusable
  # directory fails in a second rather than after ten megabytes.
  local bin_dir go_gobin go_gopath
  if [ -n "${GHTMX_BIN_DIR:-}" ]; then
    bin_dir="$GHTMX_BIN_DIR"
  elif command -v go >/dev/null 2>&1; then
    # One call, both values: `go env A B` prints them in order.
    { read -r go_gobin; read -r go_gopath; } < <(go env GOBIN GOPATH)
    if [ -n "$go_gobin" ]; then
      bin_dir="$go_gobin"
    elif [ -n "$go_gopath" ]; then
      bin_dir="$go_gopath/bin"
    else
      bin_dir="$HOME/.local/bin"
    fi
  else
    bin_dir="$HOME/.local/bin"
  fi

  case "$bin_dir" in
    "~"*) fail "GHTMX_BIN_DIR is $bin_dir — an environment variable does not expand ~; write the path out or use \$HOME" ;;
  esac
  mkdir -p "$bin_dir" || fail "could not create $bin_dir"
  [ -w "$bin_dir" ] ||
    fail "$bin_dir is not writable; re-run with sudo or set GHTMX_BIN_DIR to a directory you own"
  # `go install` rejects a relative GOBIN, and an absolute path is what
  # the closing advice needs to print anyway.
  bin_dir=$(cd "$bin_dir" && pwd)

  # --- download and verify ------------------------------------------------

  local archive expected actual
  archive="ghtmx_${bare}_${os}_${arch}.tar.gz"
  tmp=$(mktemp -d)
  trap cleanup EXIT

  echo "downloading $archive ($tag)"
  download "$releases_url/download/$tag/$archive" "$tmp/$archive" ||
    fail "could not download $archive — does release $tag exist?"
  download "$releases_url/download/$tag/checksums.txt" "$tmp/checksums.txt" ||
    fail "could not download checksums.txt for $tag"

  expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$tmp/checksums.txt")
  [ -n "$expected" ] || fail "checksums.txt has no entry for $archive"
  actual=$(sha256_of "$tmp/$archive")
  if [ "$actual" != "$expected" ]; then
    fail "checksum mismatch for $archive: expected $expected, got $actual"
  fi
  echo "checksum verified"

  # --- install ------------------------------------------------------------

  tar -xzf "$tmp/$archive" -C "$tmp" ghtmx ||
    fail "$archive does not contain a ghtmx binary"
  # Staged in the destination directory under a unique name, then
  # renamed: rename(2) is atomic within a filesystem, so a concurrent
  # run and a reader of the old binary both stay consistent.
  staged=$(mktemp "$bin_dir/.ghtmx.XXXXXX")
  install -m 0755 "$tmp/ghtmx" "$staged"
  mv -f "$staged" "$bin_dir/ghtmx"
  staged=""
  echo "installed $bin_dir/ghtmx"

  # --- gopls --------------------------------------------------------------

  local gopls_installed=0
  if [ -n "${GHTMX_SKIP_GOPLS:-}" ]; then
    echo "skipping gopls (GHTMX_SKIP_GOPLS is set)"
  elif [ "$os" != "$host_os" ] || [ "$arch" != "$host_arch" ]; then
    # go install builds for the host, which is not what was asked for.
    echo "skipping gopls: this is a cross-install for $os/$arch"
  elif ! command -v go >/dev/null 2>&1; then
    echo
    echo "warning: no Go toolchain found, so gopls was not installed."
    echo "  ghtmx works, but the language server's embedded-Go features"
    echo "  (completion, hover, and go to definition inside Go expressions)"
    echo "  stay off until you install it:"
    echo
    echo "      go install golang.org/x/tools/gopls@$gopls_version"
  else
    echo "installing gopls $gopls_version"
    # A gopls build can fail for reasons that have nothing to do with
    # ghtmx — no network, a blocked proxy, GOFLAGS. ghtmx is already
    # installed by now, so say what happened and carry on to the PATH
    # advice rather than dying halfway through.
    if GOBIN="$bin_dir" go install "golang.org/x/tools/gopls@$gopls_version"; then
      echo "installed $bin_dir/gopls"
      gopls_installed=1
    else
      echo
      echo "warning: installing gopls failed. ghtmx is installed and works;"
      echo "  embedded-Go features stay off until gopls is there. Retry with:"
      echo
      echo "      GOBIN=$bin_dir go install golang.org/x/tools/gopls@$gopls_version"
    fi
  fi

  # --- PATH ---------------------------------------------------------------

  local rc
  echo
  case ":$PATH:" in
    *":$bin_dir:"*)
      echo "$bin_dir is on your PATH."
      ;;
    *)
      # $SHELL is the login shell, which is the one whose startup file
      # a new terminal will read — the right guess even though it is not
      # necessarily the shell running this script.
      case "${SHELL##*/}" in
        zsh) rc="~/.zshrc" ;;
        bash) rc="~/.bashrc" ;;
        fish) rc="~/.config/fish/config.fish" ;;
        *) rc="your shell's startup file" ;;
      esac
      echo "warning: $bin_dir is NOT on your PATH, so editors will not find ghtmx."
      echo "  Add this line to $rc and open a new shell:"
      echo
      if [ "$rc" = "~/.config/fish/config.fish" ]; then
        echo "      fish_add_path \"$bin_dir\""
      else
        echo "      export PATH=\"$bin_dir:\$PATH\""
      fi
      echo
      echo "  This script does not edit startup files for you."
      ;;
  esac

  # Only the host build is runnable here; a cross-install is not.
  local reported
  if [ "$os" = "$host_os" ] && [ "$arch" = "$host_arch" ]; then
    reported=$("$bin_dir/ghtmx" version 2>&1) ||
      fail "the installed binary does not run: $reported"
    echo "ghtmx version: $reported"
  fi
  if [ "$gopls_installed" = 1 ]; then
    echo "gopls: $bin_dir/gopls"
  fi

  # --- VS Code extension --------------------------------------------------

  # Last, because it is the only optional step that asks a question, and
  # asking it before the install is done would put the answer in front of
  # output the user still needs to read. A cross-install is excluded:
  # installing an extension into the VS Code on this machine, for a
  # binary built for another one, is not what was asked for.
  if [ "$os" = "$host_os" ] && [ "$arch" = "$host_arch" ]; then
    offer_vscode_extension "$releases_url" "$tag" "$interactive"
  fi

  if [ "$host_os" = darwin ]; then
    echo
    echo "note: VS Code launched from Finder or the Dock does not inherit PATH"
    echo "  changes made in shell startup files. If the extension still cannot"
    echo "  find ghtmx, set the ghtmx.path setting to $bin_dir/ghtmx."
  fi
}

main "$@"
