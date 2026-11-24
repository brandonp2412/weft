package matrix

import (
	"context"
	"errors"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Crypto state changes are serialized with sync. mautrix's cross-signing cache
// is not safe to mutate concurrently with device-list processing. Decryption
// is owned here too, rather than in the helper's asynchronous session waiter.
type manualCryptoSyncer struct {
	mautrix.Syncer
	mautrix.ExtensibleSyncer
}

func (b *Backend) decryptEvent(ctx context.Context, evt *event.Event) {
	decrypted, e := b.Crypto.Decrypt(ctx, evt)
	if e == nil {
		decrypted.Mautrix.EventSource |= event.SourceDecrypted
		b.handleEvent(ctx, decrypted)
		return
	}
	if errors.Is(e, cryptohelper.NoSessionFound) {
		c := evt.Content.AsEncrypted()
		session := string(c.SessionID)
		if time.Since(b.keyRequests[session]) > time.Minute {
			b.keyRequests[session] = time.Now()
			// Dispatch through the worker after the current sync/crypto operation ends.
			select {
			case b.tasks <- func(ctx context.Context) {
				b.Crypto.RequestSession(ctx, evt.RoomID, c.SenderKey, c.SessionID, evt.Sender, "")
			}:
			default:
			}
		}
	}
}
func (b *Backend) StartVerification(ctx context.Context, to id.UserID) (id.VerificationTransactionID, error) {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.Verify.StartVerification(ctx, to)
}
func (b *Backend) AcceptVerification(ctx context.Context, txn id.VerificationTransactionID) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.Verify.AcceptVerification(ctx, txn)
}
func (b *Backend) ConfirmVerification(ctx context.Context, txn id.VerificationTransactionID) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.Verify.ConfirmSAS(ctx, txn)
}
func (b *Backend) CancelVerification(ctx context.Context, txn id.VerificationTransactionID, code event.VerificationCancelCode, reason string) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.Verify.CancelVerification(ctx, txn, code, reason)
}
func (b *Backend) Devices(ctx context.Context) (map[id.DeviceID]*id.Device, error) {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	devices, e := b.Crypto.Machine().FetchKeys(ctx, []id.UserID{b.Client.UserID}, true)
	return devices[b.Client.UserID], e
}
