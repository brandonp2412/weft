# weft

Weft is a keyboard-driven Matrix client for the terminal. It runs as a single executable and keeps your device identity, encryption keys, conversations, and queued messages between launches.

It is written in Go and uses mautrix-go, Bubble Tea v2, Lip Gloss, and SQLite.

## Build and run

You need **Go 1.27.4 or newer**, a C compiler for SQLite, and a UTF-8 terminal. Weft is tested on Linux. There are process locks for macOS/Unix and Windows, but those platforms have not been tested.

Build it and try the demo:

```sh
make
./bin/weft --demo
```

The demo runs with temporary sample data and makes no network requests. It deletes that data when you exit.

Sign in with a password:

```sh
./bin/weft login --user @you:example.org
./bin/weft
```

Weft finds your homeserver from your Matrix ID. You can also set it yourself:

```sh
./bin/weft login --homeserver https://matrix.example.org --user @you:example.org
```

Weft prompts for your password and hides it as you type. It does not save passwords or accept them as command-line arguments.

The Makefile uses `-tags goolm` to select mautrix's Go Olm implementation. Use the same flag if you build or test manually. Without it, mautrix uses libolm and needs its native headers.

```sh
go build -tags goolm -o bin/weft ./cmd/weft
go run -tags goolm ./cmd/weft --demo
make install                 # installs to ~/.local/bin/weft
```

SQLite uses CGO. You do not need a Rust compiler.

## Browser login

If your homeserver uses native OAuth:

```sh
./bin/weft login --oauth --user @you:example.org
```

Open the authorization URL that weft prints in a browser on the same machine. Weft uses a loopback callback, random state, PKCE S256, and a dedicated Matrix device scope. It rotates refresh tokens automatically.

Some providers restrict client registration. Use `--oauth-client-id CLIENT_ID` for a registered native client, or `--client-uri URL` if the provider requires your published application's URL. The callback path is `/callback`, and the native-client registration must allow an ephemeral loopback port.

For legacy Matrix SSO:

```sh
./bin/weft login --sso --homeserver https://matrix.example.org --user @you:example.org
```

For a device access token:

```sh
./bin/weft login --token --homeserver https://matrix.example.org
```

Weft hides the token as you type. A token from a device already set up in another client cannot restore that client's encryption identity. Use password or browser login where possible so weft gets its own device.

## Encryption and recovery

After signing in, verify your new device with one of these commands:

- `/verify` asks another device to verify this one. Accept the request there, compare all the symbols or numbers, and confirm on both clients.
- `/recover` asks for your existing recovery key, verifies this device, and restores your encrypted Megolm backup from the server.
- `/recover-passphrase` does the same using a secret-storage passphrase.
- `/setup-keys` creates cross-signing, secret storage, and a server key backup for a **fresh account**. It prompts for the account password and shows a recovery key to save in your password manager. It refuses to overwrite existing encryption identities, secret storage, or backups. This command requires password-based interactive authentication. If your server does not support that, set up encryption in another client and use `/recover`.

Weft uploads new encryption sessions to an existing trusted server backup automatically. It never silently replaces your encryption identity or backup. It periodically retries messages that are waiting for keys. Use `/retry` to retry decryption and queued sends yourself.

Weft encrypts messages and uploaded files in encrypted rooms. Reactions and room metadata are not encrypted by this client, following Matrix's protocol behavior. Weft relies on mautrix-go for cryptography and device trust. It has not had an independent security audit.

## Configuration

Weft reads preferences from `$XDG_CONFIG_HOME/weft/config.toml`, or
`~/.config/weft/config.toml` when `XDG_CONFIG_HOME` is unset. Use
`weft --config /path/to/config.toml` to choose another file, including for
`--demo`. Restart weft after changing the file. If it is missing, weft uses the defaults.
Invalid values and unknown keys cause an error at startup.

Copy [config.example.toml](config.example.toml) to get started. You can set the
keyboard mode, sidebar visibility, initial history size, history page size,
mouse scroll speed, typing notifications, and automatic read receipts.
`read_receipts = false` disables automatic marking; `/read` still works.
Preferences are separate from session credentials and the data directory.

`keyboard_mode = "normal"` uses the bindings in [Keyboard](#keyboard). With
`keyboard_mode = "vi"`, weft starts in navigation mode:

| Key | Action in vi navigation mode |
| --- | --- |
| `i` or Enter / `a` | Insert before / after the cursor |
| `ZZ` | Quit from vi normal mode |
| `I` / `A` | Insert at first nonblank / end of line |
| `o` / `O` | Open a line below / above |
| Escape | Return from composing to normal mode |
| `j` / `k` | Focus next / previous chat when the draft is empty; move down / up within a draft otherwise |
| `h` / `l` | Move left / right within a draft |
| `w` / `b` / `e` | Next word / previous word / end of word |
| `0` / `^` / `$` | Start / first nonblank / end of line |
| `gg` / `G` | First / last chat with an empty draft; beginning / end of a draft otherwise |
| `d` / `c` / `y` + motion | Delete / change / yank text covered by a motion |
| `dd` / `cc` / `yy` | Delete / change / yank a line |
| `D` / `C` | Delete / change to end of line |
| `x` | Delete a character |
| `p` / `P` | Paste the internal register after / before the cursor or line |
| `u` / Ctrl+R | Undo / redo draft edits |
| `:` | Start a slash command when the draft is empty |
| `/` | Search messages |

Prefix motions and operators with a count, such as `3j`, `2w`, or `2dd`.
The status bar shows `VI NORMAL` or `VI INSERT`. Chat selection changes
immediately as you navigate. Moving within a draft keeps the same chat selected;
`j`/`k` also follow wrapped display lines. Ctrl+K opens the room picker,
Ctrl+P/N switch chats even with a draft, and Ctrl+↑/↓ select messages.
Page Up/Down scroll history.

In insert mode, typing and sending work as usual. Escape returns to normal
mode before a second Escape cancels a reply or edit. `:` preserves a nonempty
draft; use insert mode to type a slash command. Undo history is cleared when
switching chats. Room and search prompts retain ordinary text entry and arrow
navigation. Visual selection, macros, and Vim Ex commands are not implemented.

## Keyboard

| Key | Action |
| --- | --- |
| Enter | Send a message or execute a command |
| Alt+Enter | Insert a newline; Shift+Enter also works in terminals that report it |
| Ctrl+K | Find and switch rooms |
| Alt+↑ / Alt+↓ | Previous / next room |
| Ctrl+P / Ctrl+N | Previous / next room |
| Ctrl+↑ / Ctrl+↓ | Select a message |
| Page Up / Page Down | Scroll history; Page Up near the start loads earlier messages |
| Ctrl+End | Return to the latest messages |
| Escape | Cancel reply/edit, leave a thread, or close a dialog |
| F1 | Open the command guide |
| Ctrl+C / Ctrl+Q | Quit |

Use the mouse wheel to scroll. In narrow terminals, the sidebar hides and you can still use the room picker. Weft keeps a separate draft for each room until you quit.

With automatic read receipts enabled, weft marks a visible conversation read when the terminal has focus and you are at the bottom of the timeline. Use `/read` to mark it read yourself.

## Commands

To refer to a message, use the `#number` shown above it, `selected` for the selected message, or a full `$eventID`. Message numbers belong to the loaded timeline and can change when you load more history.

| Command | Action |
| --- | --- |
| `/reply #3 [text]` | Send a reply, or enter reply mode |
| `/edit #3 [text]` | Edit your own message, or load it into the composer |
| `/thread #3 [text]` | Send a thread reply, or view a cached thread |
| `/back` | Return to the room timeline |
| `/me action` | Send an emote |
| `/react #3 emoji` | Add a reaction |
| `/redact #3 [reason]` | Delete a message, subject to room permissions |
| `/upload /path/to/file` | Upload an attachment; paths may contain spaces |
| `/download #3 /save/path` | Download and decrypt an attachment; existing files are never overwritten |
| `/search [query]` | Search cached messages, then jump to a result |
| `/history` | Load another page of room history |
| `/retry` | Retry queued sends and encrypted messages |
| `/discard #3` | Remove a queued send; a request already in flight may still be delivered |
| `/read` | Send a read marker |
| `/rooms` | Open the room picker |
| `/join #alias:server` | Join a room; `/join` accepts the selected invitation |
| `/leave confirm` | Leave the selected room or decline its invitation |
| `/create room name` | Create a private encrypted room |
| `/dm @user:server` | Create a private encrypted direct conversation |
| `/invite @user:server` | Invite someone to this room |
| `/members` | List joined members |
| `/name new name` | Change the room name |
| `/topic new topic` | Change the room topic |
| `/verify [@user:server]` | Request interactive SAS verification |
| `/accept`, `/confirm`, `/cancel` | Control the active verification |
| `/recover`, `/recover-passphrase` | Restore encryption keys through hidden input |
| `/setup-keys` | Set up encryption for a fresh account |
| `/devices` | Show your devices and stored trust state |
| `/whoami` | Show account, device, and fingerprint |
| `/help`, `/quit` | Open help / quit |

Start a normal message with `//` to send a leading `/`. In the verification dialog, `a` accepts a request, `y` confirms matching symbols, and `n` rejects it. Escape only hides the dialog; `/cancel` cancels the verification.

Attachments are handled in memory and limited to 64 MiB. Your server may have a smaller limit.

Search checks the latest 10,000 cached messages in the room and shows up to 100 matches. Thread views use cached messages too, so loading older room history may reveal more replies.

Weft displays messages as terminal-safe plain text. Outgoing messages include Markdown formatting for other Matrix clients.

## Local data and accounts

The default data directory is `$XDG_DATA_HOME/weft`, or `~/.local/share/weft`. `WEFT_HOME` or a global `--data-dir` flag overrides it:

```sh
./bin/weft --data-dir ~/.local/share/weft-work login --user @you:work.example
./bin/weft --data-dir ~/.local/share/weft-work
```

Each directory holds one account and device, and only one process can open it at a time. Use separate directories for multiple accounts.

| File | Contents |
| --- | --- |
| `session.json` | Account, access/refresh tokens, and the local crypto-store pickle key |
| `crypto.db` | mautrix encryption sessions, devices, trust, and room crypto state |
| `weft.db` | Cached conversations, relations, members, outbox, and sync checkpoints |
| `weft.log` | SDK warnings and errors |
| `weft.lock` | Advisory process lock |

Only the owner can access the directory and its session and database files. **Local message history and credentials are not encrypted at rest.** The pickle key is stored alongside the databases; use disk encryption and treat backups of this directory as sensitive. Do not copy a live device directory to another running client.

To sign out:

```sh
./bin/weft logout
```

Logging out revokes the session and moves the local directory to a sibling directory with a `.logged-out-TIMESTAMP` suffix. The archive keeps your history and keys, but tokens are removed from its session file. Quitting weft without logging out keeps you signed in.

## Development and validation

```sh
make test                    # unit, protocol-mock, layout, and race checks
make check                   # go vet and formatting
make integration             # actual Synapse, E2EE and restart tests
```

The integration script needs Docker, curl, and a running Docker daemon. It starts a pinned official Synapse image with its own container and volume, creates temporary local accounts, and cleans up when it finishes. The server listens on loopback at a random port. Set `WEFT_SYNAPSE_IMAGE` to test another image.

The integration suite checks password login, invitations, encrypted messages on the wire, replies, threads, edits, reactions, encrypted file transfer, read markers, cross-signing recovery, server key-backup restoration, SAS verification, device persistence, offline-send recovery, fresh-account encryption setup, trusted backup upload, redaction, and history gaps after limited syncs. Mock-server tests cover legacy SSO and native OAuth, including PKCE, callback-state validation, token rotation, persistence, and revocation. OAuth and SSO still need testing against your specific identity provider.

See [the architecture notes](docs/architecture.md) for the package boundaries and persistence model.

## Current scope

Weft currently supports text chat. Calls, account registration, a spaces hierarchy, push and desktop notifications, terminal image previews, polls, and administration tools are not implemented.