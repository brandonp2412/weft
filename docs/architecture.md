# Architecture

`cmd/weft` handles login, logout, process locks, data-directory selection, and the terminal lifecycle. `internal/config` stores credentials through a private temporary file and atomic rename. `internal/matrix` owns the mautrix client, encryption, verification, sync processing, attachments, history, backups, and send workers. `internal/store` owns the SQLite conversation model. `internal/tui` owns presentation and keyboard state.

## State flow

The Matrix syncer persists room metadata and events, then commits the next sync token. mautrix normally saves its token before processing events; weft's sync-store adapter defers the durable checkpoint until its syncer has completed event writes. Replaying an interrupted batch is safe because events and relations use Matrix event IDs as primary keys.

The backend sends coalesced change notifications to Bubble Tea. The UI reloads a bounded timeline, projects edits/reactions/redactions, wraps message blocks, and displays the visible lines. Scrolled views retain an event anchor when new messages or backfilled history arrive. Composer edits do not rebuild the timeline. The default loaded timeline contains 150 messages and grows when history is requested.

## Encryption concurrency

mautrix's cross-signing caches can be mutated by device-list syncs as well as key setup/recovery. Weft serializes sync processing and its SDK crypto operations through one mutex. Verification callbacks queue follow-up work rather than re-entering the helper while its transaction lock is held.

Weft owns encrypted-event decryption instead of enabling cryptohelper's asynchronous waiters. Raw encrypted events are persisted before decryption. A successful decryption replaces the placeholder. Missing-session requests are rate-limited, and cached ciphertext is retried periodically and after recovery. This keeps crypto cache mutation under the same serialization policy.

Protocol, cryptographic primitives, session handling, cross-signing, and SAS verification remain in mautrix-go. The build uses its `goolm` implementation. SQLite is the only required native build component.

## Persistence and sends

The application database uses SQLite WAL, foreign-key support, a busy timeout, and one connection. Each queued send is stored before network I/O and retains its Matrix transaction ID across retries and restarts. Sends preserve order within each room; a failed room does not block other rooms. A stored server event or successful persisted local echo acknowledges the outbox entry. Editing events are acknowledged through the relations table as well as normal messages through the message table.

Edits, reactions, and redactions are stored independently of their targets. A late-arriving target is projected with its existing relations. Edits must match the original sender. Relations must match the target's room. Redaction tombstones remain effective when an event is replayed or backfilled. Search uses the projected message body and excludes redacted messages.

A limited incremental sync records a durable history-gap token. The maintenance worker pages backward until it reaches the previously cached boundary, independently of the user's oldest-history pagination cursor. Normal history requests skip historical room-state changes so backfill does not overwrite current membership, names, or topics.

## Authentication

Password and legacy SSO login create a dedicated device and persist the returned credentials. SSO uses an ephemeral loopback listener with a random callback path. Native OAuth uses dynamic or pre-registered native-client metadata, a random state, PKCE S256, Matrix API/device scopes, and a loopback callback. The SDK refreshes OAuth tokens and invokes an atomic persistence callback before adopting rotated credentials. Logout revokes the legacy session or OAuth token grant and archives local data.

## Backup and attachment handling

Existing server key backups must pass the SDK's trust validation before new sessions are uploaded. Sessions are encrypted to the backup public key, sent in batches, and marked with the uploaded backup version afterward. Recovery unlocks secret storage, imports cross-signing keys, signs this device, downloads the backup, and retries cached ciphertext. Fresh-account setup refuses existing cross-signing, default secret storage, and backups.

Attachments are capped at 64 MiB. Uploads to encrypted rooms encrypt the bytes before uploading them and place the encryption metadata inside the encrypted message. Downloads use authenticated Matrix media endpoints, enforce the size limit, verify encrypted-file integrity, and create files exclusively with owner-only permissions.

## Testing boundaries

Unit tests cover durable outbox/checkpoints, out-of-order relations, redaction replay, cross-room relation isolation, terminal-control filtering, private session permissions, room drafts, scroll anchoring, help navigation, hidden secret paste, and layout bounds. HTTP mocks exercise SSO and native OAuth. The isolated Synapse suite exercises actual encrypted protocol behavior and runs with Go's race detector.

The integration suite uses several weft instances. Cross-client interoperability with Element, external identity providers, very large accounts, and other operating systems still requires validation.
