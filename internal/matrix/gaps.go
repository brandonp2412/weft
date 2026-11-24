package matrix

import (
	"context"
	"database/sql"
	"errors"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (b *Backend) fillGap(ctx context.Context) error {
	b.cryptoMu.Lock()
	defer b.cryptoMu.Unlock()
	var room, token string
	var until int64
	e := b.Store.DB.QueryRow("SELECT room_id,token,until_ts FROM history_gaps ORDER BY until_ts LIMIT 1").Scan(&room, &token, &until)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	resp, e := b.Client.Messages(ctx, id.RoomID(room), token, "", mautrix.DirectionBackward, nil, 100)
	if e != nil {
		return e
	}
	done := resp.End == "" || resp.End == token || len(resp.Chunk) == 0
	for _, evt := range resp.Chunk {
		if evt.StateKey != nil {
			continue
		}
		if evt.Timestamp < until {
			done = true
		}
		evt.RoomID = id.RoomID(room)
		_ = evt.Content.ParseRaw(evt.Type)
		b.handleEvent(ctx, evt)
		if evt.Type == event.EventEncrypted {
			b.decryptEvent(ctx, evt)
		}
	}
	b.mu.Lock()
	e = b.persistErr
	b.persistErr = nil
	b.mu.Unlock()
	if e != nil {
		return e
	}
	if done {
		_, e = b.Store.DB.Exec("DELETE FROM history_gaps WHERE room_id=? AND token=?", room, token)
	} else {
		_, e = b.Store.DB.Exec("UPDATE history_gaps SET token=? WHERE room_id=? AND token=?", resp.End, room, token)
	}
	b.Changed()
	return e
}
