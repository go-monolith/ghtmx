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
# ...@latest`. Clone the repository and read it if you want more.
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

  if [ "$host_os" = darwin ]; then
    echo
    echo "note: VS Code launched from Finder or the Dock does not inherit PATH"
    echo "  changes made in shell startup files. If the extension still cannot"
    echo "  find ghtmx, set the ghtmx.path setting to $bin_dir/ghtmx."
  fi
}

main "$@"
