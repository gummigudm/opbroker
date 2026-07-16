#!/usr/bin/env bash
# opbroker installer.
#
# Downloads the release artifact for the current platform, verifies its
# SHA256 against the published SHA256SUMS file, extracts the binary, and
# installs it to $GDOT_DIR/bin (if set) or ~/.local/bin.
#
# Usage:
#   curl -fsSL https://github.com/gummigudm/opbroker/releases/latest/download/install.sh | bash
#
# Env vars / flags:
#   OPBROKER_VERSION=vX.Y.Z    pin a specific release (default: latest)
#   OPBROKER_INSTALL_DIR=/path override install location
#   --version vX.Y.Z           same as OPBROKER_VERSION
#   --dir /path                same as OPBROKER_INSTALL_DIR
#   --local-tarball /path.tgz  skip download; install from a local tarball.
#                              If a SHA256SUMS file sits next to it, the
#                              checksum is verified against that entry.
set -euo pipefail

REPO="gummigudm/opbroker"

# Parse flags.
LOCAL_TARBALL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version)       OPBROKER_VERSION="$2"; shift 2 ;;
    --dir)           OPBROKER_INSTALL_DIR="$2"; shift 2 ;;
    --local-tarball) LOCAL_TARBALL="$2"; shift 2 ;;
    -h|--help)
      # Print the top-of-file comment block: everything from line 2 up to the
      # first line that isn't a comment (i.e. `set -euo pipefail`).
      sed -n '2,/^[^#]/{/^[^#]/q; s/^# \{0,1\}//p;}' "$0"
      exit 0
      ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

# Detect platform.
UNAME_S="$(uname -s)"
UNAME_M="$(uname -m)"
case "$UNAME_S/$UNAME_M" in
  Darwin/arm64) TARGET="darwin-arm64" ;;
  *)
    echo "opbroker: unsupported platform: $UNAME_S/$UNAME_M" >&2
    echo "  Currently supported: Darwin/arm64 (macOS on Apple Silicon)." >&2
    exit 1
    ;;
esac

# Resolve install dir.
if [ -n "${OPBROKER_INSTALL_DIR:-}" ]; then
  INSTALL_DIR="$OPBROKER_INSTALL_DIR"
elif [ -n "${GDOT_DIR:-}" ]; then
  INSTALL_DIR="$GDOT_DIR/bin"
else
  INSTALL_DIR="$HOME/.local/bin"
fi

TARBALL="opbroker-${TARGET}.tar.gz"

# Work in a temp dir; clean it on exit.
tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t opbroker)
trap 'rm -rf "$tmpdir"' EXIT

if [ -n "$LOCAL_TARBALL" ]; then
  # ---- Local-tarball mode ----
  if [ ! -f "$LOCAL_TARBALL" ]; then
    echo "opbroker: local tarball not found: $LOCAL_TARBALL" >&2
    exit 1
  fi
  VERSION="local"
  echo "opbroker installer"
  echo "  source:    $LOCAL_TARBALL"
  echo "  platform:  $TARGET"
  echo "  dest dir:  $INSTALL_DIR"
  echo

  # Stage tarball into tmpdir under the expected name.
  cp "$LOCAL_TARBALL" "$tmpdir/$TARBALL"

  # Verify against a sibling SHA256SUMS if present. Otherwise, warn and skip.
  sibling_sums="$(dirname "$LOCAL_TARBALL")/SHA256SUMS"
  if [ -f "$sibling_sums" ]; then
    cp "$sibling_sums" "$tmpdir/SHA256SUMS"
    echo "==> Verifying checksum against $sibling_sums"
    cd "$tmpdir"
    expected=$(grep " $TARBALL\$" SHA256SUMS | awk '{print $1}')
    if [ -z "$expected" ]; then
      echo "opbroker: $TARBALL not listed in $sibling_sums" >&2
      exit 1
    fi
    actual=$(shasum -a 256 "$TARBALL" | awk '{print $1}')
    if [ "$expected" != "$actual" ]; then
      echo "opbroker: SHA256 mismatch" >&2
      echo "  expected: $expected" >&2
      echo "  actual:   $actual" >&2
      exit 1
    fi
  else
    echo "==> No sibling SHA256SUMS found next to $LOCAL_TARBALL — skipping checksum"
    cd "$tmpdir"
  fi
else
  # ---- Remote-download mode ----

  # Resolve version.
  if [ -n "${OPBROKER_VERSION:-}" ]; then
    VERSION="$OPBROKER_VERSION"
  else
    # Follow the /releases/latest redirect and pull the tag from the final URL.
    latest_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
    VERSION="${latest_url##*/}"
    if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
      echo "opbroker: could not determine latest release tag from $latest_url" >&2
      exit 1
    fi
  fi

  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

  echo "opbroker installer"
  echo "  version:   $VERSION"
  echo "  platform:  $TARGET"
  echo "  dest dir:  $INSTALL_DIR"
  echo

  cd "$tmpdir"

  echo "==> Downloading $TARBALL"
  curl -fsSL -o "$TARBALL" "$BASE_URL/$TARBALL"

  echo "==> Downloading SHA256SUMS"
  curl -fsSL -o SHA256SUMS "$BASE_URL/SHA256SUMS"

  echo "==> Verifying checksum"
  expected=$(grep " $TARBALL\$" SHA256SUMS | awk '{print $1}')
  if [ -z "$expected" ]; then
    echo "opbroker: $TARBALL not listed in SHA256SUMS" >&2
    exit 1
  fi
  actual=$(shasum -a 256 "$TARBALL" | awk '{print $1}')
  if [ "$expected" != "$actual" ]; then
    echo "opbroker: SHA256 mismatch" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
fi

echo "==> Extracting"
tar xzf "$TARBALL"
if [ ! -f opbroker ]; then
  echo "opbroker: tarball did not contain an 'opbroker' binary" >&2
  exit 1
fi

echo "==> Installing to $INSTALL_DIR/opbroker"
mkdir -p "$INSTALL_DIR"
mv opbroker "$INSTALL_DIR/opbroker"
chmod +x "$INSTALL_DIR/opbroker"

# macOS Gatekeeper: strip quarantine + ad-hoc sign so a freshly-downloaded
# binary isn't SIGKILL'd on first run.
if [ "$UNAME_S" = "Darwin" ]; then
  xattr -c "$INSTALL_DIR/opbroker" 2>/dev/null || true
  codesign --force --sign - "$INSTALL_DIR/opbroker" 2>/dev/null || true
fi

echo
echo "installed opbroker $VERSION → $INSTALL_DIR/opbroker"

# PATH check.
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo
    echo "note: $INSTALL_DIR is not on your PATH."
    echo "  Add this to your shell rc file:"
    echo "    export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
