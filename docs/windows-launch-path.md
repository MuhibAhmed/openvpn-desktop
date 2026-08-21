# The Windows launch path, as verified

Everything here was confirmed empirically against OpenVPN 2.5.5 (Windows-MSVC) on
Windows 11, from an **unelevated** process, using `cmd/spike`. Where behaviour
contradicts the obvious reading of the docs, the note says so.

Run the gate yourself:

```
go run ./cmd/spike -loopback -timeout 15s     # full path, reaches CONNECTED
go run ./cmd/spike -selftest -debug           # no peer; echoes every wire line
```

## How a tunnel gets started

The app never elevates. It asks the **OpenVPN Interactive Service**
(`OpenVPNServiceInteractive`, installed and set to Automatic by the OpenVPN MSI)
to launch openvpn.exe on its behalf.

```
app  --[named pipe]-->  interactive service  --[spawns]-->  openvpn.exe
 |                              ^                                |
 |                              +---[msg-channel]----------------+
 +--[TCP management interface]-----------------------------------+
```

`openvpn.exe` runs as the *invoking user*, not as SYSTEM. Privileged operations
(open the tunnel adapter, set the address, set MTU, add routes) are delegated
back to the service over the message channel the service passes as
`--msg-channel <handle>`. Confirmed in the log as
`interactive service msg_channel=584` and `IPv4 MTU set to 1500 ... using service`.

## Pipe protocol

Pipe: `\\.\pipe\openvpn\service`. **Must** be switched to
`PIPE_READMODE_MESSAGE` — the protocol is message-framed, not a byte stream.

Startup message, written in exactly one `WriteFile`, three NUL-terminated
UTF-16LE strings:

```
workingdir \0  options \0  stdin \0
```

The service builds the command line as `openvpn <options> --msg-channel <n>`,
converts the third field from UTF-16 to UTF-8, and writes it to openvpn's stdin.

Replies use one three-line shape for both success and failure:

| Outcome | Format | Example |
| --- | --- | --- |
| Started | `0x00000000\n0x%08x\n%s` | code 0, **PID in hex**, `Process ID` |
| Failed | `0x%08x\n%s\n%s` | code, failing function, description |

The service **disconnects the pipe when openvpn.exe exits**, which is our
process-death signal. A crash while starting shows up as
`0x20000000 | OpenVPN exited with error: exit code = 1`.

## Two things that are mandatory and not obvious

**`--service <event-name> 0` is required.** Without it openvpn tries to read the
management password from a console. The service starts it without one, so it
dies instantly with exit code 1 and no explanation on the pipe. With it, openvpn
reads the password from stdin. The event is a named Windows event object created
with `CreateEvent`; signalling it is a shutdown path that does not depend on the
management connection. See `svcpipe.ExitEvent`.

**`--log <path>` is worth passing even though we read logs over the management
interface.** The service does not forward openvpn's stdout, so if openvpn dies
before the management port opens, the log file is the only account of why. Log
paths outside the registry `log_dir` are accepted.

## Management interface

Launch options we use, all of them on the service's whitelist:

```
--config "<name>.ovpn"                 relative to workingdir
--management 127.0.0.1 <port> stdin    password over stdin, never on disk
--management-hold                      so we can subscribe before anything happens
--management-query-passwords           credential prompts come to us, not a console
--auth-retry interact                  re-prompt on bad credentials instead of exiting
--service <event> 0
--log "<path>"
--verb 3
```

Handshake: openvpn writes `ENTER PASSWORD:` **with no trailing newline**, so the
reader cannot be purely line-based. Reply with the password, then expect
`SUCCESS: password is correct` and the `>INFO:` greeting.

### Writing to the socket destroys queued notifications

This is the single most important thing on this page. From `man_read()` in
`openvpn/src/openvpn/manage.c`:

```c
command_line_add(man->connection.in, buf, len);
buffer_list_reset(man->connection.out);
```

**The moment input arrives, openvpn discards everything it had queued but not yet
flushed.** `>STATE:`, `>LOG:` and — the dangerous one — `>PASSWORD:` all go
through that same queue.

`man_read` does loop over every complete line in the buffer, so a batched write
is processed in full. What batching buys is *fewer resets*: one write is one
reset, two writes are two chances to destroy something.

The rules that follow, in order of how much they hurt when broken:

1. **Send nothing after answering a credential prompt.** openvpn's very next act
   is often to ask the next question — typically the private key passphrase.
   A command sent at that moment deletes the prompt, openvpn blocks forever in
   `management_query_user_pass` waiting for an answer nobody saw, and the UI
   shows a spinner with no error. On disconnect openvpn finally reports
   `could not read Private Key username/password/ok/string from management
   interface`. This is a real bug that shipped and was fixed; it is pinned by
   `TestSessionSendsNothingElseWhenAnsweringCredentials`.
2. **Send a logically single action as one write.** `username` and `password` go
   out together via `mgmt.Client.Credentials`, never as two commands.
3. **Do not send commands while openvpn is initialising.** A redundant
   `hold release` from the `>HOLD:` handler silently wiped the
   `>STATE:ASSIGN_IP` transition and about twenty log lines.
4. **Send as few commands as possible, one at a time, consuming each reply**
   before sending the next.

Do *not* "recover" by polling `state` after sending something. That was the
original mitigation here and it is what caused (1): the poll is itself a write.
Take history in the same exchange as the subscription instead — `state on all` —
and rely on notifications after that.

Because a lost prompt is unrecoverable from the client's side, `Session` also
runs a stall watchdog: no prompt, no state change and no progress for 45s in a
pre-connected phase sets `Status.Stalled`, so the UI can explain itself instead
of spinning forever.

There is one genuine recovery route if it is ever needed. `man_welcome()`
re-sends `persist.special_state_msg` when a management client connects, so a
pending `>PASSWORD:` query is re-announced to a *newly connected* client:

```c
static void
man_welcome(struct management *man)
{
    msg(M_CLIENT, ">INFO:OpenVPN Management Interface Version %d ...");
    if (man->persist.special_state_msg)
    {
        msg(M_CLIENT, "%s", man->persist.special_state_msg);
    }
}
```

Reconnecting the management socket therefore recovers a lost prompt. We do not
do this yet; avoiding the loss is cheaper than recovering from it.

### Two reply shapes, and getting them wrong cascades

| Kind | Terminator | Commands |
| --- | --- | --- |
| Simple | `SUCCESS:` / `ERROR:` | `log on`, `bytecount N`, `hold release`, `username`, `password`, `signal` |
| Payload | bare `END` | `state`, `state on all`, `log N`, `status` |

Payload commands send the status line **first**, then the payload, then `END`:

```
-> state on all
<- SUCCESS: real-time state notification set to ON
<- 1787182514,CONNECTING,,,,,,
<- END
```

Stopping at `SUCCESS:` leaves the payload queued, and then every later command
reads the previous command's leftovers — the failure is silent and shifts by one.
Hence `mgmt.Command` (SUCCESS-terminated) versus `mgmt.Query` (END-terminated).

`state on all` rather than `state on` is deliberate: it subscribes and returns
the history in one exchange, which is the only way to be sure the early
transitions are not missed.

`log on` deliberately does *not* ask for history — at `--verb 4` openvpn's cache
is hundreds of lines of option dump.

## The hold has to be released every time, not once

`--management-hold` makes openvpn wait for permission before it starts. What is
easy to miss is that it holds **again after every soft restart** -- a rejected
passphrase, a failed authentication, a ping timeout -- and each of those holds
needs its own release:

```
>PASSWORD:Verification Failed: 'Private Key'
>STATE:...,RECONNECTING,private-key-password-failure,,,,,
>HOLD:Waiting for hold release:5
```

That last line is openvpn asking to try again. Miss it and the tunnel never
retries: the UI sits on "Reconnecting" forever and openvpn logs nothing further,
which is indistinguishable from a hang. This shipped, and it is pinned by
`TestSessionReleasesHoldAfterRestart`.

The release is therefore driven by the `>HOLD:` notification rather than sent
once at startup, which gives exactly one release per hold. Releasing twice is its
own bug -- see the output-queue rule above -- so the two constraints together
rule out both the naive fixes.

The trailing number is openvpn's own backoff hint in seconds (`0` at launch, a
few seconds after a failure). Honouring it is what keeps a profile with bad
credentials from becoming a tight retry loop, so `holdDelay` waits that long,
capped, before releasing.

## Credentials: openvpn asks more than once

A profile with `auth-user-pass` **and** an encrypted private key produces **two
independent prompts**, in whatever order openvpn happens to need them:

```
>PASSWORD:Need 'Auth' username/password
>PASSWORD:Need 'Private Key' password
```

They are different secrets and must not be confused. Two consequences that both
caused real bugs:

- **They need separate vault entries.** `creds.Slot` keeps them apart;
  `SlotAuth` deliberately maps to the bare profile id so entries written before
  slots existed are still found.
- **The dialog must be remounted per prompt** (`key={status.prompt.id}`).
  Reusing the React component carries the first prompt's password into the
  second as the answer, and carries a ticked "remember" with it, which saves the
  wrong secret into the vault. openvpn then reports
  `SIGUSR1[soft,private-key-password-failure]` and reconnects in a loop.

### A saved secret is answered without asking

When something is already stored, `Session` answers openvpn directly and never
publishes a `Prompt`, so no dialog appears — the same behaviour as the GUI this
replaces. Three rules keep that from turning into a trap:

- **One automatic attempt per realm per connection.** `Session.answered` records
  every realm that has had an answer sent, by us or by the user. If openvpn asks
  again, the stored value was rejected, so the prompt goes to the user instead.
  Without this a stale passphrase loops forever with nothing on screen.
- **Never for challenges.** A one-time code is worthless the second time, so
  `PromptStaticChallenge` and `PromptDynamicChallenge` always ask.
- **A retry prompt starts empty.** `SavedCredentialsForCurrent` returns nothing
  when `Prompt.Retry` is set, because offering back the value that was just
  rejected invites the user to submit it again.

### The old GUI may be answering a prompt you never see

The community GUI saves credentials per profile under
`HKCU\Software\OpenVPN-GUI\configs\<config name>`, and **only prompts for the
ones it has not saved**:

| Value | Contents |
| --- | --- |
| `username` / `username-enc` | account username, raw UTF-16 or DPAPI-protected |
| `auth-data` | account password, DPAPI-protected |
| `key-data` | **private key passphrase**, DPAPI-protected |
| `entropy` | `ENTROPY_LEN + 1` = 17 bytes, the optional entropy for the above |

This produces a genuinely confusing situation. Someone who ticked "save" on the
key passphrase years ago is asked for exactly one secret and reasonably believes
that is all their profile needs -- so a replacement client that asks for the
passphrase is asking for something they cannot produce. That is not a
hypothetical; it is what happened here.

`internal/legacygui` reads those values so the switch does not lose them. Two
details matter, and both fail silently if wrong:

- The protected plaintext is the WCHAR string **including its terminating NUL**
  (`(wcslen(password) + 1) * sizeof(WCHAR)`).
- The entropy blob is built with `strlen()`, so the stored trailing NUL is
  **excluded** — `cbData` is 16, not 17.

**A blank password is legitimate.** Plenty of servers authenticate on the
certificate plus a username. openvpn supports it explicitly, from
`man_query_password`:

```c
if (!string[0]) /* allow blank passwords to be passed through using the blank_up tag */
{
    string = blank_up;
}
```

So the UI must never require a password to submit the form. It required one, and
users whose server has no password could not connect at all.

`auth-nocache` in a profile means openvpn re-queries credentials rather than
caching them, so these prompts can appear again on a reconnect.

## Where profiles may live — the unresolved constraint

`openvpnserv/validate.c` gates non-admin callers two ways:

1. **Option whitelist.** Only `auth-retry, config, log, log-append, management,
   management-forget-disconnect, management-hold, management-query-passwords,
   management-query-proxy, management-signal, management-up-down, mute, setenv,
   service, verb, pull-filter, script-security`. Every networking option —
   `dhcp-option`, `block-outside-dns`, `route`, `redirect-gateway` — must
   therefore live **inside the .ovpn file**, never on the command line.

2. **Config location.** `CheckConfigPath` accepts a config file only if it sits
   under the registry `config_dir` with no `..` in the remainder:

   ```c
   config_dir = s->config_dir;   /* HKLM\SOFTWARE\OpenVPN\config_dir */
   if (wcsncmp(config_dir, config_file, wcslen(config_dir)) == 0
       && wcsstr(config_file + wcslen(config_dir), L"..") == NULL)
       return TRUE;
   ```

   There is **no** per-user fallback. `%USERPROFILE%\OpenVPN\config` is a GUI
   convention, not something the service honours.

Both checks are skipped entirely for members of `ovpn_admin_group` (default:
local Administrators). Note that group membership is resolved by group lookup,
not from the filtered token, so an **unelevated** member of Administrators is
still authorized. That is why the spike succeeds from a user-profile directory on
a developer machine and would fail for a standard user.

`config_dir` on a default install is `C:\Program Files\OpenVPN\config`, which a
standard user cannot write to. Resolving this is a prerequisite for the
"works for non-technical people" goal; see the plan's open items.
