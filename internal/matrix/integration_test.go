//go:build integration

package matrix

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"weft/internal/config"
	"weft/internal/store"
)

func eventually(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out: " + what)
}
func register(t *testing.T, ctx context.Context, hs, user string) *config.Config {
	t.Helper()
	cli, e := mautrix.NewClient(hs, "", "")
	if e != nil {
		t.Fatal(e)
	}
	_, uia, e := cli.Register(ctx, &mautrix.ReqRegister[any]{Username: user, Password: "weft-test-password", InhibitLogin: true})
	if e != nil && uia == nil {
		t.Fatal(e)
	}
	if uia != nil {
		_, _, e = cli.Register(ctx, &mautrix.ReqRegister[any]{Username: user, Password: "weft-test-password", InhibitLogin: true, Auth: map[string]any{"type": "m.login.dummy", "session": uia.Session}})
		if e != nil {
			t.Fatal(e)
		}
	}
	c, e := Login(ctx, hs, "@"+user+":weft.test", "weft-test-password", "", false, nil)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func openTest(t *testing.T, ctx context.Context, dir string, c *config.Config) *Backend {
	t.Helper()
	if e := config.Prepare(dir); e != nil {
		t.Fatal(e)
	}
	b, e := Open(ctx, dir, c)
	if e != nil {
		t.Fatal(e)
	}
	b.Start()
	select {
	case <-b.synced:
	case <-time.After(25 * time.Second):
		t.Fatal("first sync timed out")
	}
	return b
}
func findBody(b *Backend, room, body string) (store.Message, bool) {
	msgs, e := b.Store.Messages(room, 1000)
	if e != nil {
		return store.Message{}, false
	}
	for _, m := range msgs {
		if m.Body == body {
			return m, true
		}
	}
	return store.Message{}, false
}
func TestMatrixLifecycle(t *testing.T) {
	hs := os.Getenv("WEFT_TEST_HOMESERVER")
	if hs == "" {
		t.Skip("run scripts/integration.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	aConfig := register(t, ctx, hs, fmt.Sprintf("alice%d", time.Now().UnixNano()))
	bConfig := register(t, ctx, hs, fmt.Sprintf("bob%d", time.Now().UnixNano()))
	aDir := t.TempDir()
	a := openTest(t, ctx, aDir, aConfig)
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	b := openTest(t, ctx, t.TempDir(), bConfig)
	defer b.Close()
	roomResp, e := a.Client.CreateRoom(ctx, &mautrix.ReqCreateRoom{Name: "Weft integration", Preset: "private_chat", Invite: []id.UserID{b.Client.UserID}, InitialState: []*event.Event{{Type: event.StateEncryption, StateKey: strptr(""), Content: event.Content{Parsed: &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}}}}})
	if e != nil {
		t.Fatal(e)
	}
	room := roomResp.RoomID.String()
	eventually(t, "invitation", func() bool { r, e := b.Store.Room(room); return e == nil && r.Membership == "invite" })
	if _, e = b.Client.JoinRoom(ctx, room, nil); e != nil {
		t.Fatal(e)
	}
	eventually(t, "both joined and encrypted", func() bool {
		ar, ae := a.Store.Room(room)
		br, be := b.Store.Room(room)
		return ae == nil && be == nil && ar.Encrypted && br.Encrypted && br.Membership == "join"
	})
	t.Log("password login, initial sync, invitation, join and encryption state passed")
	if e = a.Text(room, "hello encrypted world", false, "", "", ""); e != nil {
		t.Fatal(e)
	}
	var first store.Message
	eventually(t, "encrypted message received", func() bool { var ok bool; first, ok = findBody(b, room, "hello encrypted world"); return ok })
	serverEvt, e := a.Client.GetEvent(ctx, id.RoomID(room), id.EventID(first.ID))
	if e != nil {
		t.Fatal(e)
	}
	if serverEvt.Type.Type != "m.room.encrypted" {
		t.Fatal("message reached server in plaintext")
	}
	eventually(t, "outbox cleared", func() bool { p, _ := a.Store.Pending(); return len(p) == 0 })
	if e = b.Text(room, "a reply", false, first.ID, "", ""); e != nil {
		t.Fatal(e)
	}
	eventually(t, "reply received", func() bool { m, ok := findBody(a, room, "a reply"); return ok && m.ReplyTo == first.ID })
	if e = a.Text(room, "thread reply", false, "", first.ID, ""); e != nil {
		t.Fatal(e)
	}
	eventually(t, "thread received", func() bool { m, ok := findBody(b, room, "thread reply"); return ok && m.ThreadRoot == first.ID })
	if e = a.Text(room, "hello edited world", false, "", "", first.ID); e != nil {
		t.Fatal(e)
	}
	eventually(t, "edit applied", func() bool {
		m, e := b.Store.Message(first.ID)
		return e == nil && m.Edited && m.Body == "hello edited world"
	})
	if e = b.React(ctx, room, first.ID, "🧶"); e != nil {
		t.Fatal(e)
	}
	eventually(t, "reaction received", func() bool {
		m, e := a.Store.Message(first.ID)
		return e == nil && strings.Contains(m.Reactions, "🧶")
	})
	t.Log("encrypted send, persistent outbox, replies, threads, edits and reactions passed")
	file := filepath.Join(t.TempDir(), "test.txt")
	content := []byte("a private attachment\n")
	if e = os.WriteFile(file, content, 0600); e != nil {
		t.Fatal(e)
	}
	if e = a.Upload(ctx, room, file); e != nil {
		t.Fatal(e)
	}
	var media store.Message
	eventually(t, "attachment received", func() bool { var ok bool; media, ok = findBody(b, room, "test.txt"); return ok })
	var evt event.Event
	if e = json.Unmarshal(media.Raw, &evt); e != nil {
		t.Fatal(e)
	}
	_ = evt.Content.ParseRaw(evt.Type)
	if evt.Content.AsMessage().File == nil {
		t.Fatal("attachment not encrypted")
	}
	downloaded := filepath.Join(t.TempDir(), "download.txt")
	if e = b.Download(ctx, media.ID, downloaded); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(downloaded)
	if !bytes.Equal(got, content) {
		t.Fatal("attachment changed in transit")
	}
	if e = b.Download(ctx, media.ID, downloaded); e == nil {
		t.Fatal("download overwrote existing file")
	}
	if e = b.MarkRead(ctx, room, media.ID); e != nil {
		t.Fatal(e)
	}
	t.Log("encrypted media upload/download, overwrite protection and read markers passed")
	// Seed real cross-signing and secret storage, then upload a valid encrypted
	// backup so a fresh device can recover without receiving the original keys.
	recovery, e := func() (string, error) {
		a.cryptoMu.Lock()
		defer a.cryptoMu.Unlock()
		key, _, e := a.Crypto.Machine().GenerateAndUploadCrossSigningKeysWithPassword(ctx, "weft-test-password", "")
		if e != nil {
			return "", e
		}
		return key, a.Crypto.Machine().VerifyWithRecoveryKey(ctx, key)
	}()
	if e != nil {
		t.Fatal("seed cross-signing:", e)
	}
	backupKey, e := backup.NewMegolmBackupKey()
	if e != nil {
		t.Fatal(e)
	}
	version, e := a.Client.CreateKeyBackupVersion(ctx, &mautrix.ReqRoomKeysVersionCreate[backup.MegolmAuthData]{Algorithm: id.KeyBackupAlgorithmMegolmBackupV1, AuthData: backup.MegolmAuthData{PublicKey: id.Ed25519(base64.RawStdEncoding.EncodeToString(backupKey.PublicKey().Bytes()))}})
	if e != nil {
		t.Fatal(e)
	}
	keyID, metadata, e := a.Crypto.Machine().SSSS.GetDefaultKeyData(ctx)
	if e != nil {
		t.Fatal(e)
	}
	ssssKey, e := metadata.VerifyRecoveryKey(keyID, recovery)
	if e != nil {
		t.Fatal(e)
	}
	secretType := event.Type{Type: "m.megolm_backup.v1", Class: event.AccountDataEventType}
	if e = a.Crypto.Machine().SSSS.SetEncryptedAccountData(ctx, secretType, []byte(base64.RawStdEncoding.EncodeToString(backupKey.Bytes())), ssssKey); e != nil {
		t.Fatal(e)
	}
	sessions, e := a.Crypto.Machine().CryptoStore.GetGroupSessionsForRoom(ctx, id.RoomID(room)).AsList()
	if e != nil {
		t.Fatal(e)
	}
	req := &mautrix.ReqKeyBackup{Rooms: map[id.RoomID]mautrix.ReqRoomKeyBackup{}}
	keys := mautrix.ReqRoomKeyBackup{Sessions: map[id.SessionID]mautrix.ReqKeyBackupData{}}
	for _, session := range sessions {
		exported, e := session.Internal.Export(session.Internal.FirstKnownIndex())
		if e != nil {
			t.Fatal(e)
		}
		encrypted, e := backup.EncryptSessionData(backupKey, backup.MegolmSessionData{Algorithm: id.AlgorithmMegolmV1, SenderKey: session.SenderKey, SenderClaimedKeys: backup.SenderClaimedKeys{Ed25519: session.SigningKey}, SessionKey: string(exported), ForwardingKeyChain: []string{}})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(encrypted)
		keys.Sessions[session.ID()] = mautrix.ReqKeyBackupData{SessionData: raw}
	}
	req.Rooms[id.RoomID(room)] = keys
	if _, e = a.Client.PutKeysInBackup(ctx, version.Version, req); e != nil {
		t.Fatal(e)
	}
	freshConfig, e := Login(ctx, hs, aConfig.UserID, "weft-test-password", "", false, nil)
	if e != nil {
		t.Fatal(e)
	}
	fresh := openTest(t, ctx, t.TempDir(), freshConfig)
	defer fresh.Close()
	if e = fresh.Recover(ctx, recovery, false); e != nil {
		t.Fatal("recovery:", e)
	}
	eventually(t, "restored old encrypted history", func() bool { m, e := fresh.Store.Message(first.ID); return e == nil && m.Body == "hello edited world" })
	t.Log("cross-signing recovery and server key-backup restoration passed")
	// SAS verification drives the actual SDK handshake on two devices.
	txn, e := a.StartVerification(ctx, a.Client.UserID)
	if e != nil {
		t.Fatal(e)
	}
	eventually(t, "verification request", func() bool {
		select {
		case v := <-fresh.Verifications:
			if v.Stage == "requested" {
				if e := fresh.AcceptVerification(ctx, v.Txn); e != nil {
					t.Fatal(e)
				}
				return true
			}
		default:
		}
		return false
	})
	var aSAS, bSAS string
	eventually(t, "SAS shown on both devices", func() bool {
		select {
		case v := <-a.Verifications:
			if v.Stage == "sas" {
				aSAS = v.SAS
			}
		default:
		}
		select {
		case v := <-fresh.Verifications:
			if v.Stage == "sas" {
				bSAS = v.SAS
			}
		default:
		}
		return aSAS != "" && bSAS != ""
	})
	if aSAS != bSAS {
		t.Fatal("SAS mismatch")
	}
	if e = a.ConfirmVerification(ctx, txn); e != nil {
		t.Fatal(e)
	}
	if e = fresh.ConfirmVerification(ctx, txn); e != nil {
		t.Fatal(e)
	}
	eventually(t, "verification done", func() bool {
		select {
		case v := <-a.Verifications:
			return v.Stage == "done"
		default:
			return false
		}
	})
	t.Log("interactive SAS verification passed")
	// Restart the sender with its original token, device ID and crypto database.
	device := a.Client.DeviceID
	fingerprint := a.Crypto.Machine().Fingerprint()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a = nil
	a = openTest(t, ctx, aDir, aConfig)
	if a.Client.DeviceID != device || a.Crypto.Machine().Fingerprint() != fingerprint {
		t.Fatal("device identity changed on restart")
	}
	if e = b.Text(room, "after restart", false, "", "", ""); e != nil {
		t.Fatal(e)
	}
	eventually(t, "decrypt after restart", func() bool { _, ok := findBody(a, room, "after restart"); return ok })
	// Simulate an offline send surviving a process restart.
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a = nil
	disk, e := store.Open(aDir)
	if e != nil {
		t.Fatal(e)
	}
	if e = disk.Enqueue(room, "weft_offline_test", event.MessageEventContent{MsgType: event.MsgText, Body: "queued while offline"}); e != nil {
		t.Fatal(e)
	}
	disk.Close()
	a = openTest(t, ctx, aDir, aConfig)
	eventually(t, "persisted send delivered", func() bool { _, ok := findBody(b, room, "queued while offline"); return ok })
	if e = a.History(ctx, room); e != nil {
		t.Fatal(e)
	}
	if e = a.Redact(ctx, room, first.ID, "test"); e != nil {
		t.Fatal(e)
	}
	eventually(t, "redaction applied", func() bool { m, e := b.Store.Message(first.ID); return e == nil && m.Redacted })
	t.Log("device restart, decryption, persisted send retry, history pagination and redaction passed")
	// Bootstrap encryption on Bob's previously fresh account, and confirm backup
	// authentication/upload without accepting an unsigned or replaced backup.
	bobRecovery, e := b.SetupKeys(ctx, "weft-test-password")
	if e != nil {
		t.Fatal("fresh account setup:", e)
	}
	if bobRecovery == "" {
		t.Fatal("missing recovery key")
	}
	if key, e := b.SetupKeys(ctx, "weft-test-password"); e == nil || key != "" {
		t.Fatal("setup replaced an existing identity")
	}
	bobVersion, e := b.Client.GetKeyBackupLatestVersion(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if bobVersion.Count == 0 {
		t.Fatal("new sessions were not backed up")
	}
	t.Log("fresh-account cross-signing, recovery key, trusted backup upload and reset protection passed")
	// A limited /sync after a long disconnect must not leave a hole in history.
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	a = nil
	for i := 0; i < 65; i++ {
		b.cryptoMu.Lock()
		_, e = b.Client.SendText(ctx, id.RoomID(room), fmt.Sprintf("offline gap %02d", i))
		b.cryptoMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	}
	a = openTest(t, ctx, aDir, aConfig)
	eventually(t, "history gap filled", func() bool { _, ok := findBody(a, room, "offline gap 00"); return ok })
	eventually(t, "history gap queue drained", func() bool {
		var count int
		_ = a.Store.DB.QueryRow("SELECT COUNT(*) FROM history_gaps").Scan(&count)
		return count == 0
	})
	t.Log("limited-sync reconnect history gap recovery passed")

}
func strptr(s string) *string { return &s }
