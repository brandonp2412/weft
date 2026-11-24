# weft

A keyboard-driven Matrix client for the terminal, written in Go with mautrix-go, Bubble Tea v2, Lip Gloss, and SQLite.

Weft runs as one executable. It saves your device identity, encryption state, conversations, and pending sends between launches.

## Build and run

Requirements: **Go 1.27.4 or newer**, a C compiler for SQLite, and a UTF-8 terminal. Linux is the tested platform. The code includes macOS/Unix and Windows process locks, but those platforms have not been validated.

```sh
make
./bin/weft --demo
```

The demo uses temporary sample data, makes no network requests, and removes its data when it exits.

Sign in with a password:

```sh
./bin/weft login --user @you:example.org
./bin/weft
```

Weft discovers the homeserver from your Matrix ID. Supply it explicitly when needed:

```sh
./bin/weft login --homeserver https://matrix.example.org --user @you:example.org
```

Passwords are prompted without echo. They are not accepted as command-line arguments or saved.

The Makefile selects mautrix's Go Olm implementation using `-tags goolm`. Keep this flag when building or testing manually; without it, mautrix selects libolm and requires its native headers.

```sh
go build -tags goolm -o bin/weft ./cmd/weft
go run -tags goolm ./cmd/weft --demo
make install                 # installs to ~/.local/bin/weft
```

SQLite uses CGO. No Rust compiler is required.

## Browser login

For homeservers using native OAuth:

```sh
./bin/weft login --oauth --user @you:example.org
```

Weft prints an authorization URL. Open it in a browser on the same machine. The flow uses a loopback callback, random state, PKCE S256, a dedicated Matrix device scope, and automatic refresh-token rotation. Some providers restrict client registration; use `--oauth-client-id CLIENT_ID` for a registered native client, or `--client-uri URL` if the provider requires your published application's URL. The callback path is `/callback`; native-client registration must allow an ephemeral loopback port.

For legacy Matrix SSO:

```sh
./bin/weft login --sso --homeserver https://matrix.example.org --user @you:example.org
```

For a device access token:

```sh
./bin/weft login --token --homeserver https://matrix.example.org
```

Token input is hidden. A token belonging to a device already initialized by another client cannot be used to reconstruct that client's encryption identity. Prefer password or browser login to create a dedicated weft device.

## Encryption and recovery

After signing in, verify the new device:

- `/verify` requests an interactive verification on another device. Accept there, then compare all symbols or numbers and confirm on both clients.
- `/recover` prompts for an existing recovery key, verifies this device, and restores your server's encrypted Megolm backup.
- `/recover-passphrase` does the same using a secret-storage passphrase.
- `/setup-keys` creates cross-signing, secret storage, and a server key backup for a **fresh account**. It prompts for the account password and shows a recovery key to save in your password manager. Existing encryption identities, secret storage, and backups are refused. Password-based interactive authentication is required for this setup command; otherwise initialize encryption in another client and use `/recover`.

Weft automatically uploads new encryption sessions to an existing trusted server backup. It never silently replaces an encryption identity or backup. Messages awaiting keys are retried periodically; `/retry` also retries decryption and queued sends.

Messages and uploaded files are encrypted in encrypted rooms. Reactions and room metadata follow Matrix's protocol behavior and are not encrypted by this client. The `◇` indicator marks encrypted rooms/messages; it does not assert that every participant's device has been verified. Cryptography and device-trust decisions are delegated to mautrix-go; weft has not undergone an independent security audit.

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

The mouse wheel scrolls. The sidebar hides in narrow terminals; the room picker remains available. Drafts are retained per room for the current process. Visible conversations are marked read while the terminal has focus and the timeline is at the bottom. `/read` marks the current conversation read explicitly.

## Commands

Message references use the `#number` displayed above a message, `selected` for the selected message, or a full `$eventID`. Numbers refer to the currently loaded timeline and can change when more history is loaded.

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

Attachments have a 64 MiB client limit and are handled in memory. Servers may impose a smaller limit. Search inspects up to the latest 10,000 cached messages in the room and shows at most 100 matches. Threads currently filter cached messages; loading older room history can reveal more replies. Message bodies are displayed as terminal-safe plain text; Markdown formatting is included in outgoing Matrix messages for other clients.

## Local data and accounts

The default data directory is `$XDG_DATA_HOME/weft`, or `~/.local/share/weft`. `WEFT_HOME` or a global `--data-dir` flag overrides it:

```sh
./bin/weft --data-dir ~/.local/share/weft-work login --user @you:work.example
./bin/weft --data-dir ~/.local/share/weft-work
```

Each directory holds one account/device and can be opened by only one process. Separate directories can be used for multiple accounts.

| File | Contents |
| --- | --- |
| `session.json` | Account, access/refresh tokens, and the local crypto-store pickle key |
| `crypto.db` | mautrix encryption sessions, devices, trust, and room crypto state |
| `weft.db` | Cached conversations, relations, members, outbox, and sync checkpoints |
| `weft.log` | SDK warnings and errors |
| `weft.lock` | Advisory process lock |

The directory is restricted to its owner, and session/database files use owner-only permissions. **Local message history and credentials are not encrypted at rest.** The pickle key is stored alongside the databases; use disk encryption and treat backups of this directory as sensitive. Do not copy a live device directory to another running client.

To sign out:

```sh
./bin/weft logout
```

This revokes the session and archives the local directory next to its original location with a `.logged-out-TIMESTAMP` suffix. Archived history and keys are retained; tokens are removed from the archived session file. Quitting normally retains the session.

## Development and validation

```sh
make test                    # unit, protocol-mock, layout, and race checks
make check                   # go vet and formatting
make integration             # actual Synapse, E2EE and restart tests
```

The integration script requires Docker, curl, and an available Docker daemon. It uses a pinned official Synapse image, creates disposable local accounts in an isolated container/volume, binds the server to loopback on a random port, and cleans up afterward. Override `WEFT_SYNAPSE_IMAGE` to test another image.

The integration suite checks password login, invitations, encrypted messages on the wire, replies, threads, edits, reactions, encrypted file transfer, read markers, cross-signing recovery, server key-backup restoration, SAS verification, device persistence, offline-send recovery, fresh-account encryption setup, trusted backup upload, redaction, and history gaps after limited syncs. Mock-server tests cover legacy SSO and native OAuth, including PKCE, callback-state validation, token rotation, persistence, and revocation. Real OAuth/SSO identity-provider interoperability still needs testing with the specific provider you use.

See [the architecture notes](docs/architecture.md) for the package boundaries and persistence model.

## Current scope

Weft is a text-chat client. It does not currently implement calls, account registration, a spaces hierarchy, push/desktop notifications, terminal image previews, polls, or administration tools. Linux and the temporary Synapse deployment are the tested environments; this is an initial implementation, not a claim of Element feature parity.
