# DTLS 1.3 &lt;-&gt; browser WebRTC demo

A minimal [Pion](https://github.com/pion/webrtc) WebRTC server that negotiates
**DTLS 1.3** (RFC 9147) with a browser, and proves **SRTP works in both
directions over DTLS 1.3**: the browser publishes a synthetic video track, the
server receives it and loops it back, and the browser renders the returned
video.

![DTLS 1.3 WebRTC demo: the browser shows the looped-back video overlaid with
"using DTLS v1.3", confirming SRTP flows both ways over DTLS 1.3.](docs/screenshot.png)

> Status: DTLS 1.3 is still unreleased in Pion. This demo builds against a
> 1.3-capable `pion/dtls` plus two small patches (see [Patches](#patches)). It is
> a proof of concept, not production code.

## Why this exists

DTLS 1.3 support lives only on unreleased `pion/dtls`. Even there, the public
keying-material exporter (`State.ExportKeyingMaterial`, RFC 5705 / RFC 8446
&sect;7.5 — the interface WebRTC uses to key DTLS-SRTP) was **DTLS 1.2-only**: it
ran the DTLS 1.2 PRF over a master secret that a 1.3 connection never populates.
The consequence is easy to hit and easy to misdiagnose:

- The DTLS 1.3 handshake completes and the connection reaches `connected`.
- But SRTP cannot be keyed, so **no media flows** — `OnTrack` stays silent, or
  SRTP auth/decrypt fails.

If you pair `pion/dtls` with SRTP (i.e. any WebRTC media path) over DTLS 1.3,
you will run into this. This demo adds the missing 1.3 exporter and shows media
flowing end to end.

## What the demo shows

- **DTLS 1.3 handshake** with a Chromium-based browser (the server logs the
  negotiated version).
- **SRTP receive** over DTLS 1.3: the browser publishes video, the server
  decrypts it (`OnTrack`).
- **SRTP send** over DTLS 1.3: the server loops each RTP packet back on its own
  track; the browser decodes and renders it.
- A **DataChannel** echo, for good measure.

## Requirements

- Go 1.24+
- `git`
- A Chromium-based browser (Chrome/Edge). Chrome negotiates DTLS 1.3 by default
  in recent versions.

## Build &amp; run

```sh
./setup.sh
./dtls13-webrtc-demo
```

Then open <http://localhost:8080> in a Chromium-based browser and click **Start**.

The server prints the negotiated DTLS version once the handshake completes:

```
DTLS handshake complete, negotiated version=1.3 (major=0xfe minor=0xfc)
```

and, once media flows:

```
SRTP media flowing over DTLS (recv): first video RTP packet decrypted ...
SRTP loopback active (send): first video RTP packet re-sent to browser
```

If the video panel renders the looped-back frames, both SRTP directions work
over DTLS 1.3.

### Compare against DTLS 1.2

```sh
DEMO_DTLS_MAX=12 ./dtls13-webrtc-demo   # forces a 1.2 ceiling
```

### Verbose logs

```sh
DEMO_LOG=debug ./dtls13-webrtc-demo     # or: trace, warn
```

## How it builds

`setup.sh` pins the Pion dependencies to a known-good, mutually consistent set
(rather than floating `main`, which drifts):

| Module        | Source                              | Patch                                    |
|---------------|-------------------------------------|------------------------------------------|
| `pion/webrtc` | released tag **v4.2.20**            | DTLS version-selector knob               |
| `pion/dtls`   | `main` @ **59f4c33** (1.3-capable)  | DTLS 1.3 keying-material exporter        |
| others        | as pulled by webrtc v4.2.20         | —                                        |

Both patched modules are wired in with `go.mod replace` onto local checkouts
under `forks/` (git-ignored).

## Patches

All patches are in [`patches/`](./patches):

- **`dtls13-srtp-exporter.patch`** (`pion/dtls`) — adds a DTLS 1.3 branch to
  `ExportKeyingMaterial` implementing the TLS 1.3 exporter (RFC 8446 &sect;7.5,
  inherited by RFC 9147): `Derive-Secret(exporter_master_secret, label, "")` then
  `HKDF-Expand-Label(secret, "exporter", Hash(context), length)`, with RFC 9147
  &sect;5.9's `dtls13` label prefix. Includes a pion&lt;-&gt;pion agreement test.
  *This is proposed upstream.*

- **`webrtc-dtls-version-selector.patch`** (`pion/webrtc`) — adds
  `SettingEngine.SetDTLSVersionRange(min, max)`, threads the range into the DTLS
  options, and logs the negotiated version. `pion/dtls` defaults to a DTLS 1.2
  floor, so 1.3 must be requested explicitly.

- **`dtls-compat-shim.go`** (`pion/dtls`) — copied into the dtls checkout as
  `compat_shim.go`. The pinned dtls commit renamed a few public symbols
  (`ClientWithOptions`/`ServerWithOptions` -&gt; `Client`/`Server`; the
  cipher-suite types moved to `pkg/crypto/ciphersuite`) after the released
  `pion/stun` (pulled transitively by webrtc) last synced. This re-adds the old
  names as thin aliases so the pinned stack compiles. It is transient
  cross-repo skew, not a defect — remove once a DTLS 1.3 release lands and the
  consumers pin it.

## License

MIT. See [LICENSE](./LICENSE). Portions are adapted from Pion examples, which are
MIT-licensed.
