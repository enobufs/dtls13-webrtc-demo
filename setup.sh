#!/usr/bin/env bash
# Build the DTLS 1.3 <-> browser WebRTC demo.
#
# Baseline: released pion/webrtc v4.2.20 (from go.mod) plus a 1.3-capable
# pion/dtls pinned to a known-good main commit, wired in with a go.mod replace.
# Two small patches make DTLS 1.3 usable:
#   1. pion/dtls  : DTLS 1.3 keying-material exporter (RFC 8446 s7.5) so SRTP can
#                   be keyed over DTLS 1.3. (Upstream PR in flight.)
#   2. pion/webrtc: a SettingEngine.SetDTLSVersionRange knob to request 1.3.
# A tiny compat shim bridges an API rename between the pinned dtls commit and the
# released pion/stun that webrtc pulls in. See README.md and patches/.
set -euo pipefail
cd "$(dirname "$0")"

# pion/dtls main commit that carries the DTLS 1.3 implementation this demo was
# verified against. Pinned (not floating main) for reproducibility.
DTLS_COMMIT="59f4c33"

WEBRTC_VERSION="v4.2.20"

FORKS="forks"
PATCHES="$(pwd)/patches"
DTLS_DIR="$FORKS/pion-dtls"

# Pion main branches are pulled straight from GitHub, so bypass the module proxy
# and checksum DB (they will not have unreleased commits).
export GOFLAGS=-mod=mod
export GOPROXY=direct
export GOSUMDB=off
export GOPRIVATE='github.com/*'
# This demo is a standalone module. Ignore any parent go.work (e.g. if the repo
# is checked out inside another Go workspace) so builds are self-contained.
export GOWORK=off

echo "==> Clone pion/dtls @ ${DTLS_COMMIT}"
mkdir -p "$FORKS"
if [ ! -d "$DTLS_DIR/.git" ]; then
  git clone -q https://github.com/pion/dtls.git "$DTLS_DIR"
fi
git -C "$DTLS_DIR" fetch -q origin
git -C "$DTLS_DIR" checkout -q "$DTLS_COMMIT"

echo "==> Apply pion/dtls patches (idempotent)"
# Reset any prior application so re-runs are clean.
git -C "$DTLS_DIR" checkout -q -- . 2>/dev/null || true
# DTLS 1.3 SRTP keying-material exporter (RFC 8446 s7.5). Without it,
# ExportKeyingMaterial is 1.2-only and SRTP-over-DTLS-1.3 cannot be keyed.
git -C "$DTLS_DIR" apply "$PATCHES/dtls13-srtp-exporter.patch"
echo "    dtls: srtp exporter (1.3) applied"
# Compat shim: re-add the pre-rename dtls symbol names that the released
# pion/stun (pulled by webrtc) still calls. Remove once a DTLS 1.3 release lands.
cp "$PATCHES/dtls-compat-shim.go" "$DTLS_DIR/compat_shim.go"
echo "    dtls: compat shim installed"

echo "==> Prepare pion/webrtc ${WEBRTC_VERSION} with the version-selector patch"
# We patch webrtc in the module cache is not possible; instead vendor a local
# copy of the two patched files via a replace onto a checkout of the tag.
WEBRTC_DIR="$FORKS/pion-webrtc"
if [ ! -d "$WEBRTC_DIR/.git" ]; then
  git clone -q https://github.com/pion/webrtc.git "$WEBRTC_DIR"
fi
git -C "$WEBRTC_DIR" fetch -q --tags origin
git -C "$WEBRTC_DIR" checkout -q "$WEBRTC_VERSION"
git -C "$WEBRTC_DIR" checkout -q -- . 2>/dev/null || true
# DTLS version selector: SettingEngine.SetDTLSVersionRange + thread it through +
# log the negotiated version. (Not yet upstreamed.)
git -C "$WEBRTC_DIR" apply "$PATCHES/webrtc-dtls-version-selector.patch"
echo "    webrtc: version selector applied"

echo "==> Wire go.mod replaces (dtls + webrtc -> local checkouts)"
go mod edit -replace github.com/pion/dtls/v3="./$DTLS_DIR"
go mod edit -replace github.com/pion/webrtc/v4="./$WEBRTC_DIR"

echo "==> go mod tidy"
go mod tidy

echo "==> Build"
go build -o dtls13-webrtc-demo .

echo
echo "OK. Run the demo:"
echo "  ./dtls13-webrtc-demo                     # negotiates DTLS 1.3 with the browser"
echo "  DEMO_DTLS_MAX=12 ./dtls13-webrtc-demo    # control: force DTLS 1.2"
echo "Then open http://localhost:8080 in a Chromium-based browser and click Start."
echo "The server logs: 'DTLS handshake complete, negotiated version=...'."
