package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Room struct {
	ID         string
	Name       string
	Topic      string
	Membership string
	Encrypted  bool
	Pinned     bool
	Unread     int
	LastTS     int64
	PrevBatch  string
	Exhausted  bool
}

func (r Room) Title() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

type Message struct {
	ID         string
	RoomID     string
	Sender     string
	Body       string
	Kind       string
	TS         int64
	ReplyTo    string
	ThreadRoot string
	Edited     bool
	Redacted   bool
	Encrypted  bool
	Pending    bool
	Error      string
	Reactions  string
	Raw        []byte
}
type Pending struct {
	Txn, RoomID string
	Content     event.MessageEventContent
	TS          int64
	Error       string
}
type Store struct{ DB *sql.DB }

func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "weft.db")
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS rooms(id TEXT PRIMARY KEY,name TEXT NOT NULL DEFAULT '',topic TEXT NOT NULL DEFAULT '',membership TEXT NOT NULL DEFAULT 'join',encrypted INTEGER NOT NULL DEFAULT 0,unread INTEGER NOT NULL DEFAULT 0,last_ts INTEGER NOT NULL DEFAULT 0,prev_batch TEXT NOT NULL DEFAULT '',exhausted INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS room_pins(room_id TEXT PRIMARY KEY REFERENCES rooms(id));
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,room_id TEXT NOT NULL,sender TEXT NOT NULL,body TEXT NOT NULL,kind TEXT NOT NULL,ts INTEGER NOT NULL,reply_to TEXT NOT NULL DEFAULT '',thread_root TEXT NOT NULL DEFAULT '',edited INTEGER NOT NULL DEFAULT 0,redacted INTEGER NOT NULL DEFAULT 0,encrypted INTEGER NOT NULL DEFAULT 0,raw BLOB);
 CREATE INDEX IF NOT EXISTS messages_room_ts ON messages(room_id,ts,id);
 CREATE TABLE IF NOT EXISTS relations(id TEXT PRIMARY KEY,room_id TEXT NOT NULL,target TEXT NOT NULL,sender TEXT NOT NULL,kind TEXT NOT NULL,body TEXT NOT NULL,ts INTEGER NOT NULL,redacted INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS relation_target ON relations(target,ts);
 CREATE TABLE IF NOT EXISTS outbox(txn TEXT PRIMARY KEY,room_id TEXT NOT NULL,content BLOB NOT NULL,ts INTEGER NOT NULL,error TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS history_gaps(room_id TEXT NOT NULL,token TEXT NOT NULL,until_ts INTEGER NOT NULL,PRIMARY KEY(room_id,token));
 CREATE TABLE IF NOT EXISTS members(room_id TEXT NOT NULL,user_id TEXT NOT NULL,name TEXT NOT NULL,PRIMARY KEY(room_id,user_id));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Get(key string) string {
	var v string
	_ = s.DB.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&v)
	return v
}
func (s *Store) Put(key, value string) error {
	_, e := s.DB.Exec("INSERT INTO kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return e
}

// Sync tokens are committed only after the sync listeners have persisted the batch.
func (s *Store) SaveNextBatch(_ context.Context, _ id.UserID, token string) error {
	return nil // The syncer commits this after all event handlers succeed.
}
func (s *Store) LoadNextBatch(_ context.Context, _ id.UserID) (string, error) {
	return s.Get("next_batch"), nil
}
func (s *Store) SaveFilterID(_ context.Context, _ id.UserID, filter string) error {
	return s.Put("filter_id", filter)
}
func (s *Store) LoadFilterID(_ context.Context, _ id.UserID) (string, error) {
	return s.Get("filter_id"), nil
}
func (s *Store) SaveRoom(_ *mautrix.Room) error { return nil }
func (s *Store) LoadRoom(roomID id.RoomID) (*mautrix.Room, error) {
	return mautrix.NewRoom(roomID), nil
}
func (s *Store) EnsureRoom(roomID string) error {
	_, e := s.DB.Exec("INSERT OR IGNORE INTO rooms(id) VALUES(?)", roomID)
	return e
}
func (s *Store) RoomField(roomID, field string, value any) error {
	switch field {
	case "name", "topic", "membership", "encrypted", "prev_batch", "exhausted", "unread":
	default:
		return fmt.Errorf("invalid room field %s", field)
	}
	if e := s.EnsureRoom(roomID); e != nil {
		return e
	}
	_, e := s.DB.Exec("UPDATE rooms SET "+field+"=? WHERE id=?", value, roomID)
	return e
}

// PinRoom caches the Matrix favourite tag for room ordering.
func (s *Store) PinRoom(roomID string, pinned bool) error {
	var err error
	if pinned {
		_, err = s.DB.Exec("INSERT OR IGNORE INTO room_pins(room_id) SELECT id FROM rooms WHERE id=?", roomID)
	} else {
		_, err = s.DB.Exec("DELETE FROM room_pins WHERE room_id=?", roomID)
	}
	return err
}
func (s *Store) Rooms() ([]Room, error) {
	rows, e := s.DB.Query("SELECT id,name,topic,membership,encrypted,unread,last_ts,prev_batch,exhausted,EXISTS(SELECT 1 FROM room_pins WHERE room_id=rooms.id) FROM rooms WHERE membership IN ('join','invite') ORDER BY EXISTS(SELECT 1 FROM room_pins WHERE room_id=rooms.id) DESC,last_ts DESC,name,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		var r Room
		if e = rows.Scan(&r.ID, &r.Name, &r.Topic, &r.Membership, &r.Encrypted, &r.Unread, &r.LastTS, &r.PrevBatch, &r.Exhausted, &r.Pinned); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Room(roomID string) (Room, error) {
	var r Room
	e := s.DB.QueryRow("SELECT id,name,topic,membership,encrypted,unread,last_ts,prev_batch,exhausted,EXISTS(SELECT 1 FROM room_pins WHERE room_id=rooms.id) FROM rooms WHERE id=?", roomID).Scan(&r.ID, &r.Name, &r.Topic, &r.Membership, &r.Encrypted, &r.Unread, &r.LastTS, &r.PrevBatch, &r.Exhausted, &r.Pinned)
	return r, e
}
func (s *Store) SetMember(roomID, user, name string) error {
	_, e := s.DB.Exec("INSERT INTO members VALUES(?,?,?) ON CONFLICT(room_id,user_id) DO UPDATE SET name=excluded.name", roomID, user, name)
	return e
}
func (s *Store) MemberName(roomID, user string) string {
	var name string
	_ = s.DB.QueryRow("SELECT name FROM members WHERE room_id=? AND user_id=?", roomID, user).Scan(&name)
	if name == "" {
		return user
	}
	return name
}
func (s *Store) SaveMessage(m Message, unread bool) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.Exec(`INSERT INTO messages(id,room_id,sender,body,kind,ts,reply_to,thread_root,encrypted,raw) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=CASE WHEN messages.redacted THEN messages.body ELSE excluded.body END,kind=excluded.kind,reply_to=excluded.reply_to,thread_root=excluded.thread_root,encrypted=excluded.encrypted,raw=excluded.raw WHERE excluded.kind != 'encrypted' OR messages.kind = 'encrypted'`, m.ID, m.RoomID, m.Sender, m.Body, m.Kind, m.TS, m.ReplyTo, m.ThreadRoot, m.Encrypted, m.Raw)
	if e != nil {
		return e
	}
	_ = res

	_, e = tx.Exec("INSERT INTO rooms(id,last_ts) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET last_ts=MAX(last_ts,excluded.last_ts)", m.RoomID, m.TS)
	if e != nil {
		return e
	}
	// Unread counts from /sync are authoritative; this flag is reserved for local backends.
	_ = unread
	return tx.Commit()
}
func (s *Store) Relation(eventID, room, target, sender, kind, body string, ts int64) error {
	_, e := s.DB.Exec("INSERT OR IGNORE INTO relations(id,room_id,target,sender,kind,body,ts) VALUES(?,?,?,?,?,?,?)", eventID, room, target, sender, kind, body, ts)
	return e
}
func (s *Store) Redact(target, redactionID, room, sender string, ts int64) error {
	// Persist tombstones even if the target arrives later through backfill.
	if e := s.Relation(redactionID, room, target, sender, "redaction", "", ts); e != nil {
		return e
	}
	_, e := s.DB.Exec("UPDATE relations SET redacted=1 WHERE id=? AND room_id=?", target, room)
	return e
}
func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	e := row.Scan(&m.ID, &m.RoomID, &m.Sender, &m.Body, &m.Kind, &m.TS, &m.ReplyTo, &m.ThreadRoot, &m.Edited, &m.Redacted, &m.Encrypted, &m.Raw)
	return m, e
}

const messageCols = "id,room_id,sender,body,kind,ts,reply_to,thread_root,edited,redacted,encrypted,raw"

func (s *Store) Message(eventID string) (Message, error) {
	m, e := scanMessage(s.DB.QueryRow("SELECT "+messageCols+" FROM messages WHERE id=?", eventID))
	if e != nil {
		return m, e
	}
	return s.decorate(m)
}
func (s *Store) Messages(roomID string, limit int) ([]Message, error) {
	rows, e := s.DB.Query("SELECT "+messageCols+" FROM (SELECT * FROM messages WHERE room_id=? ORDER BY ts DESC,id DESC LIMIT ?) ORDER BY ts,id", roomID, limit)
	if e != nil {
		return nil, e
	}
	var out []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i := range out {
		out[i], e = s.decorate(out[i])
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Store) decorate(m Message) (Message, error) {
	var n int
	e := s.DB.QueryRow("SELECT COUNT(*) FROM relations WHERE target=? AND room_id=? AND kind='redaction'", m.ID, m.RoomID).Scan(&n)
	if e != nil {
		return m, e
	}
	if n > 0 {
		m.Redacted = true
		m.Body = "[message deleted]"
		return m, nil
	}
	var edited string
	e = s.DB.QueryRow(`SELECT body FROM relations r WHERE target=? AND room_id=? AND kind='edit' AND sender=? AND redacted=0 AND NOT EXISTS(SELECT 1 FROM relations d WHERE d.kind='redaction' AND d.target=r.id AND d.room_id=r.room_id) ORDER BY ts DESC,id DESC LIMIT 1`, m.ID, m.RoomID, m.Sender).Scan(&edited)
	if e == nil {
		m.Body = edited
		m.Edited = true
	} else if e != sql.ErrNoRows {
		return m, e
	}
	rows, e := s.DB.Query(`SELECT body,COUNT(DISTINCT sender) FROM relations r WHERE target=? AND room_id=? AND kind='reaction' AND redacted=0 AND NOT EXISTS(SELECT 1 FROM relations d WHERE d.kind='redaction' AND d.target=r.id AND d.room_id=r.room_id) GROUP BY body ORDER BY body`, m.ID, m.RoomID)
	if e != nil {
		return m, e
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if e = rows.Scan(&key, &count); e != nil {
			return m, e
		}
		if m.Reactions != "" {
			m.Reactions += "  "
		}
		m.Reactions += fmt.Sprintf("%s %d", key, count)
	}
	return m, rows.Err()
}
func (s *Store) Enqueue(roomID, txn string, content event.MessageEventContent) error {
	b, e := json.Marshal(content)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO outbox(txn,room_id,content,ts) VALUES(?,?,?,?)", txn, roomID, b, time.Now().UnixMilli())
	return e
}
func (s *Store) Pending() ([]Pending, error) {
	rows, e := s.DB.Query("SELECT txn,room_id,content,ts,error FROM outbox ORDER BY ts,txn")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		var b []byte
		if e = rows.Scan(&p.Txn, &p.RoomID, &b, &p.TS, &p.Error); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &p.Content); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) PendingError(txn string, err error) error {
	_, e := s.DB.Exec("UPDATE outbox SET error=? WHERE txn=?", err.Error(), txn)
	return e
}
func (s *Store) RemovePending(txn string) error {
	_, e := s.DB.Exec("DELETE FROM outbox WHERE txn=?", txn)
	return e
}
func (s *Store) Search(roomID, query string) ([]Message, error) {
	rows, e := s.DB.Query("SELECT "+messageCols+" FROM messages WHERE room_id=? ORDER BY ts DESC LIMIT 10000", roomID)
	if e != nil {
		return nil, e
	}
	var all []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		all = append(all, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	var out []Message
	for _, m := range all {
		m, e = s.decorate(m)
		if e != nil {
			return nil, e
		}
		if !m.Redacted && containsFold(m.Body, query) {
			out = append(out, m)
			if len(out) == 100 {
				break
			}
		}
	}
	return out, nil
}

func (s *Store) HasEvent(eventID string) (bool, error) {
	var count int
	e := s.DB.QueryRow("SELECT (SELECT COUNT(*) FROM messages WHERE id=?)+(SELECT COUNT(*) FROM relations WHERE id=?)", eventID, eventID).Scan(&count)
	return count > 0, e
}
