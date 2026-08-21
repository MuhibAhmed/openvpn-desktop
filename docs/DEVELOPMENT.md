# Developing VPN Desktop

Go + [Wails v3](https://v3.wails.io) backend, React + TypeScript frontend.

For what the app is and how to install a release, see the
[README](../README.md). This file is for working on it.

## Prerequisites

- **Go 1.25+**
- **Node 22+**
- **OpenVPN 2.5+** with the Interactive Service enabled (the default), which is
  what the app drives
- The Wails v3 CLI:

  ```sh
  go install github.com/wailsapp/wails/v3/cmd/wails3@latest
  ```

## Build and run

```sh
wails3 dev      # hot-reloading dev build
wails3 build    # produces bin/openvpn-desktop.exe
```

After changing any exported method or type in `services/`, regenerate the
frontend bindings:

```sh
wails3 generate bindings -i './...'
```

The version shown in Settings comes from `internal/version`. A build can
override it without editing the file:

```sh
go build -ldflags "-X github.com/MuhibAhmed/openvpn-desktop/internal/version.Current=1.0.1-rc1" .
```

`build/windows/info.json` carries the version compiled into the executable's
Windows version resource, and `build/config.yml` the packaging metadata. Both
need bumping alongside a release.

One trap in `info.json`: the language block must be keyed `"0409"` (US English).
The Wails template ships `"0000"`, and with that key `wails3 generate syso`
succeeds, writes a `.syso`, and produces **no version resource at all** - the
executable's Properties dialog comes out blank with nothing reported anywhere.
Check it after a release build:

```powershell
(Get-Item bin\openvpn-desktop.exe).VersionInfo
```

## Testing

```sh
go test ./internal/...
```

The interesting parts are covered without needing a VPN, a server, or admin
rights:

- `internal/mgmt` - parser tests against transcripts captured verbatim from a
  real openvpn 2.5.5 session, plus every credential-prompt shape.
- `internal/profile` - parsing, certificate inlining, and the store, including
  the sidecar-files case and malformed input.
- `internal/vpn` - the whole connection state machine driven by a scripted fake
  management server: username/password, static challenge, dynamic (CRV1)
  challenge, auth rejection, answering from stored secrets, asking again when a
  stored secret is rejected, hold release after a soft restart, fatal-error
  translation, reconnects and clean shutdown.
- `internal/creds` - round-trips a password through the real Windows Credential
  Manager, then removes it.
- `internal/brand` - fails if either colour stops being a real presence at 16px,
  or if the connected badge starts eating into the chevron.

`go test -race` needs a C compiler, which the development machine did not have;
the concurrency-heavy packages are instead run repeatedly (`-count=4`).

### Exercising the real thing

`cmd/spike` drives the Windows launch path with no UI in the way. It is how the
protocol was worked out and it stays useful as a diagnostic:

```sh
go run ./cmd/spike -loopback -timeout 15s   # local peer, static key, reaches CONNECTED
go run ./cmd/spike -tls -timeout 25s        # TLS peer + encrypted key: exercises the passphrase prompt
go run ./cmd/spike -selftest -debug         # no peer; echoes every wire line
go run ./cmd/spike -config <file.ovpn>      # a real profile
```

`-loopback` starts a second openvpn as a `dev null` peer on localhost, so the
tunnel genuinely comes up and reports `CONNECTED` with live byte counters,
without a server or a network. `-tls` goes further: it generates a throwaway CA
and an **encrypted** client key, so openvpn asks for the passphrase over the
management interface and the credential path is exercised against the real thing.

One gap worth knowing: a config needing *both* `auth-user-pass` and a key
passphrase cannot be reproduced locally, because `--auth-user-pass` requires
`--pull`, which means real client/server mode, which needs a second tunnel
adapter on the machine. That ordering is covered by the fake-server tests in
`internal/vpn` instead.

## Layout

```
main.go, tray.go        Wails app, window, system tray
services/               the API the frontend sees; Wails generates TS from it
internal/
  svcpipe/              OpenVPN Interactive Service named-pipe client
  mgmt/                 management-interface client and parser
  vpn/                  Runner (platform launcher) + Session/Manager state machine
  ovpn/                 locating and describing the OpenVPN installation
  profile/              .ovpn parsing, certificate inlining, profile store
  brand/                the logo, drawn once and rendered to tray icons and .ico
  creds/                credentials in the OS vault
  legacygui/            reads credentials the community GUI already saved
  settings/             preferences and autostart
cmd/spike/              end-to-end diagnostic for the launch path
cmd/genicons/           regenerates the icon files from internal/brand
docs/                   how the Windows launch path actually behaves
frontend/               React + TypeScript UI
```

## Things worth knowing before changing the backend

Collected in [windows-launch-path.md](windows-launch-path.md), all verified by
observation rather than inferred. The four that will cost you an afternoon:

- `--service <event> 0` is **mandatory**. Without it openvpn reads the management
  password from a console it does not have and exits 1 with nothing on the pipe.
- **Writing to the management socket destroys whatever openvpn had queued but not
  yet flushed** (`buffer_list_reset` in `man_read`). Never send anything right
  after answering a credential prompt: the next thing openvpn queues is usually
  the private key passphrase, and deleting that prompt hangs the connection with
  no error at all.
- Payload commands (`state`, `log N`, `status`, and the `on all` forms) send the
  status line first, then the payload, then a bare `END`. Stopping at `SUCCESS:`
  leaves the payload queued and silently shifts every later reply by one.
- **The hold must be released for every hold, not once.** openvpn holds at launch
  *and again after every soft restart*, so a rejected credential leaves it
  waiting forever with nothing in the log, which looks exactly like a hang.

## Brand

The mark is a bold white chevron on a blue tile: the V of VPN, and a funnel
narrowing to a point. Blue `#2563eb` is the identity; white is its counterpart.

Green is reserved for one thing, a live tunnel, so `--ok` is a separate token
from `--accent` in the stylesheet. Status and identity should never compete for
the same colour, and green already means "protected" to everyone.

The geometry is defined twice, deliberately, because the two consumers need
different formats:

- `internal/brand` renders it as raster for the tray icon, the green connected
  badge, and `build/windows/icon.ico`.
- `BrandMark` in `frontend/src/icons.tsx` is the same shape as SVG, for the
  sidebar, the empty state and the favicon.

Change one and change the other. After editing the raster side, regenerate the
icon files:

```sh
go run ./cmd/genicons
```

That writes `build/windows/icon.ico`, `build/appicon.png`, and the magnified
tray figure used in the README.

The design constraint was 16px. A shield with a keyhole turns to mush at that
size, so the mark is one high-contrast silhouette with no interior detail.

## Cutting a release

1. Bump the version in `internal/version/version.go`, `build/config.yml` and
   `build/windows/info.json`.
2. Add the release to [CHANGELOG.md](../CHANGELOG.md).
3. `go test ./internal/...` and `wails3 build`.
4. `go run ./cmd/release` to produce the zip under `dist/`.
5. Tag it (`git tag -a v1.0.0`), push, and attach the zip to a GitHub release.
