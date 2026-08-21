# Changelog

All notable changes to VPN Desktop are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-08-20

The first release. Windows only, and verified by connecting real tunnels
end to end through the UI.

### Added

**Profiles**

- Import a `.ovpn` file by dragging it onto the window, or by choosing the file
  from *Add a profile*, which creates the profile folder itself.
- Certificates and keys referenced as sibling files (`ca`, `cert`, `key`,
  `tls-auth`, `tls-crypt`) are rewritten as inline blocks, making each profile a
  single self-contained file.
- Profiles are stored in `%USERPROFILE%\OpenVPN\config\<name>\`, the layout the
  community OpenVPN GUI uses, so existing profiles are listed without importing.
- Rename, duplicate and delete, with the server and sign-in method summarised per
  profile.

**Connecting**

- Launches `openvpn.exe` through the OpenVPN Interactive Service, so connecting
  needs no UAC prompt and the app never runs elevated.
- Full management-interface handling: connection state, live throughput, the
  openvpn log, credential prompts, `NEED-OK` prompts and clean shutdown.
- Connection state surfaced as a single power dial, with uptime, byte counters
  and a throughput graph.
- Fatal errors and authentication failures translated into plain sentences,
  including the common "all TAP-Windows adapters are currently in use".
- Severity-coloured, copyable connection log.
- A watchdog reports a stalled connection after 45 seconds instead of waiting
  forever.

**Credentials**

- Username and password, private-key passphrase, static challenge and dynamic
  (CRV1) challenge, so two-factor profiles work.
- Secrets stored in the Windows Credential Manager, with the account password
  and the private-key passphrase held in separate slots.
- A stored secret is submitted silently; the dialog only reappears if openvpn
  rejects it.
- Credentials previously saved by the community OpenVPN GUI are read and reused,
  including its DPAPI-encrypted `auth-data` and `key-data` entries.
- Blank passwords are accepted, for profiles that legitimately have none.

**Living in the tray**

- System tray icon that gains a green badge while the tunnel is up.
- Closing the window keeps the app running with the connection alive.
- Start with Windows, starting minimised to the tray.
- Optional auto-connect of a chosen profile on start.
- Light, dark, or follow Windows.

**Branding**

- Logo, favicon, app icon and tray icons all rendered from one description in
  `internal/brand`, with tests that fail if the mark stops being legible at 16px.

### Known limitations

- OpenVPN 2.5 or later must be installed separately. Bundling it is the next
  thing planned.
- Requires membership of the local Administrators group, because of where the
  Interactive Service will accept a config from.
- Loose `.ovpn` files in the config root are not listed; only subfolders are
  scanned.
- No DNS control, split tunnelling or kill switch yet.
- Not code-signed, so Windows SmartScreen warns on first run.
- Windows only. The process launcher already sits behind an interface for macOS.

[1.0.0]: https://github.com/MuhibAhmed/openvpn-desktop/releases/tag/v1.0.0
