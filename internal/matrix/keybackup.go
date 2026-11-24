package matrix

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/id"
)

// Backup uploads new sessions only to an existing backup authenticated by the
// SDK against a trusted device/cross-signing signature. It never creates or
// replaces a user's backup or encryption identity.
func (b *Backend) Backup(ctx context.Context) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.backup(ctx)
}
func (b *Backend) backup(ctx context.Context) error {
	mach := b.Crypto.Machine()
	version, e := mach.GetAndVerifyLatestKeyBackupVersion(ctx, nil)
	if errors.Is(e, mautrix.MNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if version == nil {
		return nil
	}
	pubBytes, e := base64.RawStdEncoding.DecodeString(strings.TrimRight(version.AuthData.PublicKey.String(), "="))
	if e != nil {
		return e
	}
	pub, e := ecdh.X25519().NewPublicKey(pubBytes)
	if e != nil {
		return e
	}
	sessions, e := mach.CryptoStore.GetGroupSessionsWithoutKeyBackupVersion(ctx, version.Version).AsList()
	if e != nil {
		return e
	}
	for start := 0; start < len(sessions); start += 100 {
		batch := sessions[start:min(start+100, len(sessions))]
		req := &mautrix.ReqKeyBackup{Rooms: map[id.RoomID]mautrix.ReqRoomKeyBackup{}}
		var uploaded []*crypto.InboundGroupSession
		for _, session := range batch {
			exported, e := session.Internal.Export(session.Internal.FirstKnownIndex())
			if e != nil {
				return e
			}
			enc, e := backup.EncryptSessionDataWithPubkey(pub, backup.MegolmSessionData{Algorithm: id.AlgorithmMegolmV1, ForwardingKeyChain: session.ForwardingChains, SenderClaimedKeys: backup.SenderClaimedKeys{Ed25519: session.SigningKey}, SenderKey: session.SenderKey, SessionKey: string(exported), SharedHistory: session.SharedHistory})
			if e != nil {
				return e
			}
			raw, e := json.Marshal(enc)
			if e != nil {
				return e
			}
			room := req.Rooms[session.RoomID]
			if room.Sessions == nil {
				room.Sessions = map[id.SessionID]mautrix.ReqKeyBackupData{}
			}
			room.Sessions[session.ID()] = mautrix.ReqKeyBackupData{FirstMessageIndex: int(session.Internal.FirstKnownIndex()), ForwardedCount: len(session.ForwardingChains), IsVerified: session.Internal.IsVerified(), SessionData: raw}
			req.Rooms[session.RoomID] = room
			uploaded = append(uploaded, session)
		}
		if _, e = b.Client.PutKeysInBackup(ctx, version.Version, req); e != nil {
			return fmt.Errorf("upload key backup: %w", e)
		}
		for _, session := range uploaded {
			session.KeyBackupVersion = version.Version
			if e = mach.CryptoStore.PutGroupSession(ctx, session); e != nil {
				return e
			}
		}
	}
	return nil
}
