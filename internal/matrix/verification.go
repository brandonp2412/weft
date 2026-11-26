package matrix

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/crypto/ssss"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (b *Backend) verification(v Verification) {
	select {
	case b.Verifications <- v:
	case <-b.ctx.Done():
	}
}
func (b *Backend) VerificationRequested(_ context.Context, txn id.VerificationTransactionID, from id.UserID, device id.DeviceID) {
	b.verification(Verification{Txn: txn, Stage: "requested", User: from.String(), Device: device.String()})
}
func (b *Backend) VerificationReady(ctx context.Context, txn id.VerificationTransactionID, device id.DeviceID, sas, scan bool, qr *verificationhelper.QRCode) {
	if sas {
		b.verification(Verification{Txn: txn, Stage: "ready", Device: device.String()})
		// SDK callbacks run while its transaction lock is held. Re-enter
		// the helper only after the callback has returned.
		select {
		case b.tasks <- func(ctx context.Context) {
			if e := b.Verify.StartSAS(ctx, txn); e != nil {
				b.notice("Start verification: "+e.Error(), true)
			}
		}:
		case <-b.ctx.Done():
		}
	} else {
		b.notice("Other device does not support SAS verification", true)
	}
}
func (b *Backend) VerificationCancelled(_ context.Context, txn id.VerificationTransactionID, code event.VerificationCancelCode, reason string) {
	b.verification(Verification{Txn: txn, Stage: "cancelled", SAS: reason})
}
func (b *Backend) VerificationDone(_ context.Context, txn id.VerificationTransactionID, method event.VerificationMethod) {
	b.verification(Verification{Txn: txn, Stage: "done"})
	b.notice("Device verified", false)
}
func (b *Backend) ShowSAS(_ context.Context, txn id.VerificationTransactionID, emojis []rune, descriptions []string, decimals []int) {
	var parts []string
	for i, r := range emojis {
		d := ""
		if i < len(descriptions) {
			d = descriptions[i]
		}
		parts = append(parts, fmt.Sprintf("%c %s", r, d))
	}
	sas := strings.Join(parts, "  ")
	if len(decimals) > 0 {
		sas += fmt.Sprintf("\nNumbers: %v", decimals)
	}
	b.verification(Verification{Txn: txn, Stage: "sas", SAS: sas})
}

// Recover verifies the device using existing secret storage, then restores the
// server's Megolm backup. It never replaces the user's cross-signing identity.
func (b *Backend) Recover(ctx context.Context, secret string, passphrase bool) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	mach := b.Crypto.Machine()
	keyID, metadata, e := mach.SSSS.GetDefaultKeyData(ctx)
	if e != nil {
		return e
	}
	var key *ssss.Key
	if passphrase {
		key, e = metadata.VerifyPassphrase(keyID, secret)
	} else {
		key, e = metadata.VerifyRecoveryKey(keyID, secret)
	}
	if e != nil {
		return fmt.Errorf("unlock secret storage: %w", e)
	}
	if e = mach.FetchCrossSigningKeysFromSSSS(ctx, key); e != nil {
		return fmt.Errorf("restore signing keys: %w", e)
	}
	if e = mach.SignOwnDevice(ctx, mach.OwnIdentity()); e != nil {
		return e
	}
	if e = mach.SignOwnMasterKey(ctx); e != nil {
		return e
	}
	if e = persistCrossSigningForVerification(ctx, mach); e != nil {
		return fmt.Errorf("save verified cross-signing state: %w", e)
	}
	secretType := event.Type{Type: "m.megolm_backup.v1", Class: event.AccountDataEventType}
	data, e := mach.SSSS.GetDecryptedAccountData(ctx, secretType, key)
	if e != nil {
		return fmt.Errorf("device verified, but could not load backup key: %w", e)
	}
	bk, e := decodeStoredBackupKey(data)
	if e != nil {
		return e
	}
	// Require the actual private key to match the current backup. A trusted
	// signature on a backup alone does not prove we can decrypt its sessions.
	info, e := b.Client.GetKeyBackupLatestVersion(ctx)
	if e != nil {
		return fmt.Errorf("load current backup: %w", e)
	}
	if info.Algorithm != id.KeyBackupAlgorithmMegolmBackupV1 ||
		info.AuthData.PublicKey != id.Ed25519(base64.RawStdEncoding.EncodeToString(bk.PublicKey().Bytes())) {
		return fmt.Errorf("recovered key does not match the current backup")
	}
	version, e := mach.DownloadAndStoreLatestKeyBackup(ctx, bk)
	if e != nil {
		return fmt.Errorf("device verified, but backup restoration failed: %w", e)
	}
	if e = mach.CryptoStore.PutSecret(ctx, id.SecretMegolmBackupV1, base64.RawStdEncoding.EncodeToString(bk.Bytes())); e != nil {
		return fmt.Errorf("save verified backup state: %w", e)
	}
	b.notice("Device verified; restored key backup "+string(version), false)
	return b.retryDecrypt(ctx)
}

// persistCrossSigningForVerification supplies the SDK's normal encrypted
// secret store. The SDK itself handles requests and checks device trust;
// Weft never forwards secrets through a custom or unverified path.
func persistCrossSigningForVerification(ctx context.Context, mach *crypto.OlmMachine) error {
	if mach.CrossSigningKeys == nil {
		return fmt.Errorf("cross-signing keys not loaded")
	}
	keys := mach.ExportCrossSigningKeys()
	for name, seed := range map[id.Secret][]byte{
		id.SecretXSMaster:      keys.MasterKey,
		id.SecretXSSelfSigning: keys.SelfSigningKey,
		id.SecretXSUserSigning: keys.UserSigningKey,
	} {
		if err := mach.CryptoStore.PutSecret(ctx, name, base64.RawStdEncoding.EncodeToString(seed)); err != nil {
			return err
		}
	}
	return nil
}

// SSSS.GetDecryptedAccountData already decodes the Matrix base64 payload.
// Older Weft releases wrote an extra base64 layer; accept that format on read
// only. New backups always use the standard 32-byte secret.
func decodeStoredBackupKey(data []byte) (*backup.MegolmBackupKey, error) {
	if len(data) == 32 {
		return backup.MegolmBackupKeyFromBytes(data)
	}
	if len(data) == 43 || len(data) == 44 {
		raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(string(data), "="))
		if err == nil && len(raw) == 32 {
			return backup.MegolmBackupKeyFromBytes(raw)
		}
	}
	return nil, fmt.Errorf("invalid backup key format")
}
