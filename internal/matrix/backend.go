package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/crypto/verificationhelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/pushrules"
	"weft/internal/config"
	"weft/internal/store"
)

type Notice struct {
	Text  string
	Error bool
}
type Verification struct {
	Txn    id.VerificationTransactionID
	Stage  string
	User   string
	Device string
	SAS    string
}
type Backend struct {
	cryptoMu                 sync.Mutex
	keyRequests              map[string]time.Time
	tasks                    chan func(context.Context)
	Client                   *mautrix.Client
	Store                    *store.Store
	Crypto                   *cryptohelper.CryptoHelper
	Verify                   *verificationhelper.VerificationHelper
	Notifications            chan store.Message
	Updates                  chan struct{}
	Notices                  chan Notice
	Verifications            chan Verification
	ctx                      context.Context
	cancel                   context.CancelFunc
	wg                       sync.WaitGroup
	wake                     chan struct{}
	synced                   chan struct{}
	syncOnce                 sync.Once
	mu                       sync.Mutex
	persistErr               error
	typing                   map[string][]string
	logFile                  *os.File
	lowPriority              map[id.RoomID]bool
	alertRules               *pushrules.PushRuleset
	alertMemberCounts        map[id.RoomID]int
	alertSettingsUnavailable bool
}

func Open(ctx context.Context, dir string, c *config.Config) (*Backend, error) {
	s, e := store.Open(dir)
	if e != nil {
		return nil, e
	}
	cli, e := mautrix.NewClient(c.Homeserver, id.UserID(c.UserID), c.AccessToken)
	if e != nil {
		s.Close()
		return nil, e
	}
	cli.DeviceID = id.DeviceID(c.DeviceID)
	configureOAuth(cli, dir, c)
	cli.Store = s
	logFile, e := os.OpenFile(filepath.Join(dir, "weft.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		s.Close()
		return nil, e
	}
	cli.Log = zerolog.New(logFile).Level(zerolog.WarnLevel).With().Timestamp().Logger()
	cli.UserAgent = "weft/0.1.0"
	runCtx, cancel := context.WithCancel(ctx)
	b := &Backend{keyRequests: map[string]time.Time{}, tasks: make(chan func(context.Context), 32), Client: cli, Store: s, Notifications: make(chan store.Message, 64), Updates: make(chan struct{}, 1), Notices: make(chan Notice, 64), Verifications: make(chan Verification, 16), ctx: runCtx, cancel: cancel, wake: make(chan struct{}, 1), synced: make(chan struct{}), typing: make(map[string][]string), logFile: logFile}
	cleanup := func(err error) (*Backend, error) { cancel(); s.Close(); logFile.Close(); return nil, err }
	ds := cli.Syncer.(*mautrix.DefaultSyncer)
	// Raw encrypted events are persisted before the helper tries decryption.
	ds.OnEvent(b.handleEvent)
	crypto, e := cryptohelper.NewCryptoHelper(cli, []byte(c.PickleKey), filepath.Join(dir, "crypto.db"))
	if e != nil {
		return cleanup(e)
	}
	b.Crypto = crypto
	crypto.CustomPostDecrypt = b.handleEvent
	cli.Syncer = manualCryptoSyncer{Syncer: ds, ExtensibleSyncer: ds}
	if e = crypto.Init(runCtx); e != nil {
		crypto.Close()
		return cleanup(fmt.Errorf("initialize encryption: %w", e))
	}
	cli.Syncer = ds
	ds.OnEventType(event.EventEncrypted, b.decryptEvent)
	cli.Crypto = crypto
	_ = os.Chmod(filepath.Join(dir, "crypto.db"), 0600)
	b.Verify = verificationhelper.NewVerificationHelper(cli, crypto.Machine(), nil, b, false, false, true)
	if e = b.Verify.Init(runCtx); e != nil {
		crypto.Close()
		return cleanup(e)
	}
	cli.Syncer = &syncer{DefaultSyncer: ds, b: b}
	return b, nil
}
func (b *Backend) Start() {
	b.wg.Add(4)
	go b.maintenanceLoop()
	go func() {
		defer b.wg.Done()
		for {
			select {
			case <-b.ctx.Done():
				return
			case task := <-b.tasks:
				b.cryptoMu.Lock()
				task(b.ctx)
				b.cryptoMu.Unlock()
			}
		}
	}()
	go func() {
		defer b.wg.Done()
		b.notice("Connecting…", false)
		if e := b.SyncRoomTags(b.ctx); e != nil && b.ctx.Err() == nil {
			b.alertSettingsUnavailable = true
			b.notice("Room preferences: "+e.Error(), true)
		}
		if e := b.Client.SyncWithContext(b.ctx); e != nil && b.ctx.Err() == nil {
			b.notice("Sync stopped: "+e.Error(), true)
		}
	}()
	go b.outboxLoop()
}
func (b *Backend) Close() error {
	b.cancel()
	b.Client.StopSync()
	b.wg.Wait()
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	e := b.Crypto.Close()
	if se := b.Store.Close(); e == nil {
		e = se
	}
	b.logFile.Close()
	return e
}
func (b *Backend) Changed() {
	select {
	case b.Updates <- struct{}{}:
	default:
	}
}
func (b *Backend) notice(text string, isError bool) {
	select {
	case b.Notices <- Notice{text, isError}:
	case <-b.ctx.Done():
	default:
	}
}
func (b *Backend) record(err error) {
	if err == nil {
		return
	}
	b.mu.Lock()
	b.persistErr = errors.Join(b.persistErr, err)
	b.mu.Unlock()
	b.notice("Storage: "+err.Error(), true)
}
func (b *Backend) Typing(room string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.typing[room]...)
}

type syncer struct {
	*mautrix.DefaultSyncer
	b *Backend
}

func (s *syncer) ProcessResponse(ctx context.Context, res *mautrix.RespSync, since string) error {
	b := s.b
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	for room, r := range res.Rooms.Join {
		b.record(b.Store.RoomField(room.String(), "membership", "join"))
		old, _ := b.Store.Room(room.String())
		if since != "" && r.Timeline.Limited && old.LastTS > 0 && r.Timeline.PrevBatch != "" {
			_, gapErr := b.Store.DB.Exec("INSERT OR IGNORE INTO history_gaps(room_id,token,until_ts) VALUES(?,?,?)", room.String(), r.Timeline.PrevBatch, old.LastTS)
			b.record(gapErr)
		}
		if old.PrevBatch == "" && r.Timeline.PrevBatch != "" {
			b.record(b.Store.RoomField(room.String(), "prev_batch", r.Timeline.PrevBatch))
		}
		if r.UnreadNotifications != nil {
			b.record(b.Store.RoomField(room.String(), "unread", r.UnreadNotifications.NotificationCount))
		}
	}
	for room := range res.Rooms.Invite {
		b.record(b.Store.RoomField(room.String(), "membership", "invite"))
	}
	for room := range res.Rooms.Leave {
		b.record(b.Store.RoomField(room.String(), "membership", "leave"))
	}
	// Only later syncs produce alerts. Ignore cached events and initial history.
	var incoming []*event.Event
	select {
	case <-b.synced:
		for _, room := range res.Rooms.Join {
			for _, evt := range room.Timeline.Events {
				_ = evt.Content.ParseRaw(evt.Type)
				if b.notificationCandidate(evt) {
					incoming = append(incoming, evt)
				}
			}
		}
	default:
	}
	if e := s.DefaultSyncer.ProcessResponse(ctx, res, since); e != nil {
		return e
	}
	b.mu.Lock()
	e := b.persistErr
	b.persistErr = nil
	b.mu.Unlock()
	if e != nil {
		return e
	}
	for room, r := range res.Rooms.Join {
		b.record(b.Store.RoomField(room.String(), "membership", "join"))
		saved, _ := b.Store.Room(room.String())
		if saved.Name == "" && len(r.Summary.Heroes) > 0 {
			names := []string{}
			for _, hero := range r.Summary.Heroes {
				names = append(names, b.Store.MemberName(room.String(), hero.String()))
			}
			b.record(b.Store.RoomField(room.String(), "name", strings.Join(names, ", ")))
		}
	}
	for room := range res.Rooms.Leave {
		b.record(b.Store.RoomField(room.String(), "membership", "leave"))
	}
	for room := range res.Rooms.Invite {
		b.record(b.Store.RoomField(room.String(), "membership", "invite"))
	}
	b.mu.Lock()
	e = b.persistErr
	b.persistErr = nil
	b.mu.Unlock()
	if e != nil {
		return e
	}
	if e = b.Store.Put("next_batch", res.NextBatch); e != nil {
		return e
	}
	b.updateAlertSettings(res, since == "")
	for _, evt := range incoming {
		if b.alertSettingsUnavailable || b.lowPriority[evt.RoomID] || !b.eventAllowsAlert(ctx, evt) {
			continue
		}
		if msg, err := b.Store.Message(evt.ID.String()); err == nil && !msg.Redacted {
			select {
			case b.Notifications <- msg:
			default:
			}
		}
	}
	b.syncOnce.Do(func() { close(b.synced) })
	b.notice("Connected", false)
	b.Changed()
	b.Wake()
	return nil
}
func (s *syncer) OnFailedSync(res *mautrix.RespSync, err error) (time.Duration, error) {
	s.b.notice("Reconnecting: "+err.Error(), true)
	return s.DefaultSyncer.OnFailedSync(res, err)
}
func (b *Backend) handleEvent(ctx context.Context, evt *event.Event) {
	if evt.RoomID == "" {
		return
	}
	if evt.Unsigned.TransactionID != "" {
		defer func() {
			if exists, e := b.Store.HasEvent(evt.ID.String()); e == nil && exists {
				b.record(b.Store.RemovePending(evt.Unsigned.TransactionID))
				b.Changed()
			}
		}()
	}
	if evt.Unsigned.RedactedBecause != nil && evt.StateKey == nil {
		redaction := evt.Unsigned.RedactedBecause
		redactionID := redaction.ID.String()
		if redactionID == "" {
			redactionID = "redaction:" + evt.ID.String()
		}
		b.record(b.Store.Redact(evt.ID.String(), redactionID, evt.RoomID.String(), redaction.Sender.String(), evt.Timestamp))
		raw, _ := json.Marshal(evt)
		b.record(b.Store.SaveMessage(store.Message{ID: evt.ID.String(), RoomID: evt.RoomID.String(), Sender: evt.Sender.String(), Body: "[message deleted]", Kind: "m.text", TS: evt.Timestamp, Raw: raw}, false))
		b.Changed()
		return
	}
	if evt.Type.Class == event.StateEventType {
		switch evt.Type {
		case event.StateRoomName:
			b.record(b.Store.RoomField(evt.RoomID.String(), "name", evt.Content.AsRoomName().Name))
		case event.StateTopic:
			b.record(b.Store.RoomField(evt.RoomID.String(), "topic", evt.Content.AsTopic().Topic))
		case event.StateCanonicalAlias:
			r, _ := b.Store.Room(evt.RoomID.String())
			if r.Name == "" {
				b.record(b.Store.RoomField(evt.RoomID.String(), "name", evt.Content.AsCanonicalAlias().Alias.String()))
			}
		case event.StateEncryption:
			b.record(b.Store.RoomField(evt.RoomID.String(), "encrypted", true))
		case event.StateMember:
			c := evt.Content.AsMember()
			b.record(b.Store.SetMember(evt.RoomID.String(), evt.GetStateKey(), c.Displayname))
			if evt.GetStateKey() == b.Client.UserID.String() {
				b.record(b.Store.RoomField(evt.RoomID.String(), "membership", string(c.Membership)))
			}
		}
		b.Changed()
		return
	}
	switch evt.Type {
	case event.AccountDataRoomTags:
		tags := evt.Content.AsTag().Tags
		_, pinned := tags[event.RoomTagFavourite]
		_, lowPriority := tags[event.RoomTagLowPriority]
		b.record(b.Store.EnsureRoom(evt.RoomID.String()))
		b.record(b.Store.PinRoom(evt.RoomID.String(), pinned))
		b.record(b.Store.SetRoomLowPriority(evt.RoomID.String(), lowPriority))
		b.Changed()
		return
	case event.EphemeralEventTyping:
		users := evt.Content.AsTyping().UserIDs
		names := []string{}
		for _, u := range users {
			if u != b.Client.UserID {
				names = append(names, b.Store.MemberName(evt.RoomID.String(), u.String()))
			}
		}
		b.mu.Lock()
		b.typing[evt.RoomID.String()] = names
		b.mu.Unlock()
		b.Changed()
		return
	case event.EventReaction:
		c := evt.Content.AsReaction()
		b.record(b.Store.Relation(evt.ID.String(), evt.RoomID.String(), c.RelatesTo.EventID.String(), evt.Sender.String(), "reaction", c.RelatesTo.Key, evt.Timestamp))
		b.Changed()
		return
	case event.EventRedaction:
		target := evt.Redacts
		if c := evt.Content.AsRedaction(); c.Redacts != "" {
			target = c.Redacts
		}
		b.record(b.Store.Redact(target.String(), evt.ID.String(), evt.RoomID.String(), evt.Sender.String(), evt.Timestamp))
		b.Changed()
		return
	case event.EventEncrypted:
		raw, _ := json.Marshal(evt)
		b.record(b.Store.SaveMessage(store.Message{ID: evt.ID.String(), RoomID: evt.RoomID.String(), Sender: evt.Sender.String(), Body: "[encrypted message — waiting for keys]", Kind: "encrypted", TS: evt.Timestamp, Encrypted: true, Raw: raw}, false))
		b.Changed()
		return
	case event.EventMessage, event.EventSticker:
		c := evt.Content.AsMessage()
		raw, _ := json.Marshal(evt)
		if c.RelatesTo != nil && c.RelatesTo.Type == event.RelReplace && c.NewContent != nil {
			b.record(b.Store.Relation(evt.ID.String(), evt.RoomID.String(), c.RelatesTo.EventID.String(), evt.Sender.String(), "edit", c.NewContent.Body, evt.Timestamp))
			b.Changed()
			return
		}
		body := c.Body
		c.RemoveReplyFallback()
		body = c.Body
		if body == "" && c.FormattedBody != "" {
			body = format.HTMLToMarkdown(c.FormattedBody)
		}
		kind := string(c.MsgType)
		if evt.Type == event.EventSticker {
			kind = "m.sticker"
		}
		reply, thread := "", ""
		if c.RelatesTo != nil {
			reply = c.RelatesTo.GetReplyTo().String()
			thread = c.RelatesTo.GetThreadParent().String()
		}
		b.record(b.Store.SaveMessage(store.Message{ID: evt.ID.String(), RoomID: evt.RoomID.String(), Sender: evt.Sender.String(), Body: body, Kind: kind, TS: evt.Timestamp, ReplyTo: reply, ThreadRoot: thread, Encrypted: evt.Mautrix.EventSource&event.SourceDecrypted != 0, Raw: raw}, false))
		if evt.Unsigned.TransactionID != "" {
			b.record(b.Store.RemovePending(evt.Unsigned.TransactionID))
		}
		b.Changed()
	}
}
func (b *Backend) Wake() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}
func (b *Backend) Queue(room string, c event.MessageEventContent) error {
	if room == "" {
		return errors.New("select a room first")
	}
	r, e := b.Store.Room(room)
	if e != nil {
		return e
	}
	if r.Membership != "join" {
		return errors.New("join this room before sending")
	}
	txn := "weft_" + config.NewKey()
	if e = b.Store.Enqueue(room, txn, c); e != nil {
		return e
	}
	b.Wake()
	b.Changed()
	return nil
}
func (b *Backend) outboxLoop() {
	defer b.wg.Done()
	select {
	case <-b.synced:
	case <-b.ctx.Done():
		return
	}
	_ = b.RetryDecrypt(b.ctx)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
		case <-b.wake:
		}
		pending, e := b.Store.Pending()
		if e != nil {
			b.notice(e.Error(), true)
			continue
		}
		blocked := map[string]bool{}
		for _, p := range pending {
			if b.ctx.Err() != nil {
				return
			}
			if blocked[p.RoomID] {
				continue
			}
			ctx, cancel := context.WithTimeout(b.ctx, 45*time.Second)
			b.cryptoMu.Lock()
			var sendContent any = &p.Content
			sendType := event.EventMessage
			room, checkErr := b.Store.Room(p.RoomID)
			var sendErr error
			if checkErr != nil {
				sendErr = checkErr
			} else if room.Encrypted {
				sendContent, sendErr = b.Crypto.Encrypt(ctx, id.RoomID(p.RoomID), event.EventMessage, &p.Content)
				sendType = event.EventEncrypted
			}
			var resp *mautrix.RespSendEvent
			if sendErr == nil {
				resp, sendErr = b.Client.SendMessageEvent(ctx, id.RoomID(p.RoomID), sendType, sendContent, mautrix.ReqSendEvent{TransactionID: p.Txn})
			}
			e := sendErr
			b.cryptoMu.Unlock()
			cancel()
			if e != nil {
				_ = b.Store.PendingError(p.Txn, e)
				blocked[p.RoomID] = true
				b.notice("Message queued; retrying: "+e.Error(), true)
				b.Changed()
				continue
			}
			raw, _ := json.Marshal(p.Content)
			evt := &event.Event{ID: resp.EventID, RoomID: id.RoomID(p.RoomID), Sender: b.Client.UserID, Timestamp: p.TS, Type: event.EventMessage, Content: event.Content{VeryRaw: raw}}
			_ = evt.Content.ParseRaw(event.EventMessage)
			if room.Encrypted {
				evt.Mautrix.EventSource |= event.SourceDecrypted
			}
			b.handleEvent(b.ctx, evt)
			// Keep a retryable record if persisting the local echo fails.
			if exists, checkErr := b.Store.HasEvent(resp.EventID.String()); checkErr == nil && exists {
				b.record(b.Store.RemovePending(p.Txn))
			}
			b.Changed()
		}
	}
}
func (b *Backend) History(ctx context.Context, room string) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	r, e := b.Store.Room(room)
	if e != nil {
		return e
	}
	if r.Exhausted {
		return nil
	}
	resp, e := b.Client.Messages(ctx, id.RoomID(room), r.PrevBatch, "", mautrix.DirectionBackward, nil, 50)
	if e != nil {
		return e
	}

	for _, evt := range resp.Chunk {
		if evt.StateKey != nil {
			continue
		}
		evt.RoomID = id.RoomID(room)
		_ = evt.Content.ParseRaw(evt.Type)
		b.handleEvent(ctx, evt)
		if evt.Type == event.EventEncrypted {
			b.decryptEvent(ctx, evt)
		}
	}
	b.mu.Lock()
	persistErr := b.persistErr
	b.persistErr = nil
	b.mu.Unlock()
	if persistErr != nil {
		return persistErr
	}
	if resp.End == "" || resp.End == r.PrevBatch || len(resp.Chunk) == 0 {
		e = b.Store.RoomField(room, "exhausted", true)
	} else {
		e = b.Store.RoomField(room, "prev_batch", resp.End)
	}
	b.Changed()
	return e
}
func (b *Backend) RetryDecrypt(ctx context.Context) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	return b.retryDecrypt(ctx)
}
func (b *Backend) retryDecrypt(ctx context.Context) error {
	rows, e := b.Store.DB.Query("SELECT raw FROM messages WHERE kind='encrypted'")
	if e != nil {
		return e
	}
	var raws [][]byte
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		raws = append(raws, raw)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, raw := range raws {
		var evt event.Event
		if json.Unmarshal(raw, &evt) != nil {
			continue
		}
		_ = evt.Content.ParseRaw(evt.Type)
		b.decryptEvent(ctx, &evt)
	}
	b.Changed()
	return nil
}

func (b *Backend) maintenanceLoop() {
	defer b.wg.Done()
	select {
	case <-b.synced:
	case <-b.ctx.Done():
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastBackup := time.Time{}
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(b.ctx, 40*time.Second)
		if e := b.fillGap(ctx); e != nil && b.ctx.Err() == nil {
			b.notice("History gap: "+e.Error(), true)
		}
		_ = b.RetryDecrypt(ctx)
		if time.Since(lastBackup) > time.Minute {
			if e := b.Backup(ctx); e != nil {
				b.Client.Log.Debug().Err(e).Msg("Key backup unavailable")
			}
			lastBackup = time.Now()
		}
		cancel()
	}
}

// Context is cancelled before stores are closed so in-flight UI work can stop.
func (b *Backend) Context() context.Context { return b.ctx }

// notificationCandidate is called before saving a sync timeline event.
func (b *Backend) notificationCandidate(evt *event.Event) bool {
	if evt.Sender == b.Client.UserID || evt.StateKey != nil || evt.Unsigned.RedactedBecause != nil {
		return false
	}
	if evt.Type != event.EventMessage && evt.Type != event.EventEncrypted && evt.Type != event.EventSticker {
		return false
	}
	if evt.Type == event.EventMessage && evt.Content.AsMessage().RelatesTo != nil && evt.Content.AsMessage().RelatesTo.Type == event.RelReplace {
		return false
	}
	exists, err := b.Store.HasEvent(evt.ID.String())
	return err == nil && !exists
}
