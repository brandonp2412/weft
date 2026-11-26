package matrix

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/crypto/signatures"
	"maunium.net/go/mautrix/crypto/ssss"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// SetupKeys bootstraps a new account. Existing identities, secret storage and
// backups are explicitly refused, so this cannot silently reset another client.
func (b *Backend) SetupKeys(ctx context.Context, password string) (string, error) {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	mach := b.Crypto.Machine()
	existing, e := mach.GetOwnCrossSigningPublicKeys(ctx)
	if e != nil {
		return "", e
	}
	if existing != nil && existing.MasterKey != "" {
		return "", errors.New("cross-signing already exists; use /verify or /recover")
	}
	if keyID, e := mach.SSSS.GetDefaultKeyID(ctx); e == nil && keyID != "" {
		return "", errors.New("secret storage already exists; use /recover")
	} else if e != nil && !errors.Is(e, mautrix.MNotFound) && !errors.Is(e, ssss.ErrNoDefaultKeyID) {
		return "", e
	}
	if version, e := b.Client.GetKeyBackupLatestVersion(ctx); e == nil && version != nil {
		return "", errors.New("key backup already exists; use /recover")
	} else if e != nil && !errors.Is(e, mautrix.MNotFound) {
		return "", e
	}
	recovery, _, e := mach.GenerateAndUploadCrossSigningKeysWithPassword(ctx, password, "")
	if e != nil {
		return recovery, e
	}
	if e = mach.VerifyWithRecoveryKey(ctx, recovery); e != nil {
		return recovery, e
	}
	if e = persistCrossSigningForVerification(ctx, mach); e != nil {
		return recovery, e
	}
	keyID, metadata, e := mach.SSSS.GetDefaultKeyData(ctx)
	if e != nil {
		return recovery, e
	}
	key, e := metadata.VerifyRecoveryKey(keyID, recovery)
	if e != nil {
		return recovery, e
	}
	bk, e := backup.NewMegolmBackupKey()
	if e != nil {
		return recovery, e
	}
	auth := backup.MegolmAuthData{PublicKey: id.Ed25519(base64.RawStdEncoding.EncodeToString(bk.PublicKey().Bytes()))}
	sig, e := mach.GetAccount().SignJSON(auth)
	if e != nil {
		return recovery, e
	}
	auth.Signatures = signatures.NewSingleSignature(b.Client.UserID, id.KeyAlgorithmEd25519, b.Client.DeviceID.String(), sig)
	if _, e = b.Client.CreateKeyBackupVersion(ctx, &mautrix.ReqRoomKeysVersionCreate[backup.MegolmAuthData]{Algorithm: id.KeyBackupAlgorithmMegolmBackupV1, AuthData: auth}); e != nil {
		return recovery, e
	}
	if e = mach.SSSS.SetEncryptedAccountData(ctx, event.Type{Type: string(id.SecretMegolmBackupV1), Class: event.AccountDataEventType}, bk.Bytes(), key); e != nil {
		return recovery, fmt.Errorf("store backup secret: %w", e)
	}
	if e = mach.CryptoStore.PutSecret(ctx, id.SecretMegolmBackupV1, base64.RawStdEncoding.EncodeToString(bk.Bytes())); e != nil {
		return recovery, e
	}
	return recovery, b.backup(ctx)
}
