package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func (b *Backend) Text(room, body string, emote bool, reply, thread, edit string) error {
	c := format.RenderMarkdown(body, true, false)
	if emote {
		c.MsgType = event.MsgEmote
	}
	if reply != "" {
		c.RelatesTo = (&event.RelatesTo{}).SetReplyTo(id.EventID(reply))
	}
	if thread != "" {
		c.RelatesTo = (&event.RelatesTo{}).SetThread(id.EventID(thread), id.EventID(thread))
	}
	if edit != "" {
		m, e := b.Store.Message(edit)
		if e != nil {
			return e
		}
		if m.RoomID != room || m.Sender != b.Client.UserID.String() {
			return errors.New("you can only edit your own messages in this room")
		}
		c.SetEdit(id.EventID(edit))
	}
	return b.Queue(room, c)
}
func (b *Backend) React(ctx context.Context, room, target, key string) error {
	c := event.ReactionEventContent{RelatesTo: event.RelatesTo{Type: event.RelAnnotation, EventID: id.EventID(target), Key: key}}
	resp, e := b.Client.SendMessageEvent(ctx, id.RoomID(room), event.EventReaction, c)
	if e != nil {
		return e
	}
	e = b.Store.Relation(resp.EventID.String(), room, target, b.Client.UserID.String(), "reaction", key, time.Now().UnixMilli())
	b.Changed()
	return e
}
func (b *Backend) Redact(ctx context.Context, room, target, reason string) error {
	resp, e := b.Client.RedactEvent(ctx, id.RoomID(room), id.EventID(target), mautrix.ReqRedact{Reason: reason})
	if e != nil {
		return e
	}
	e = b.Store.Redact(target, resp.EventID.String(), room, b.Client.UserID.String(), time.Now().UnixMilli())
	b.Changed()
	return e
}
func (b *Backend) MarkRead(ctx context.Context, room, target string) error {
	if target == "" {
		return nil
	}
	if e := b.Client.SetReadMarkers(ctx, id.RoomID(room), mautrix.ReqSetReadMarkers{Read: id.EventID(target), FullyRead: id.EventID(target)}); e != nil {
		return e
	}
	e := b.Store.RoomField(room, "unread", 0)
	b.Changed()
	return e
}

const MaxAttachment = 64 << 20

func (b *Backend) Upload(ctx context.Context, room, path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return errors.New("attachment must be a regular file")
	}
	if info.Size() > MaxAttachment {
		return errors.New("attachment exceeds 64 MiB")
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxAttachment+1))
	if e != nil {
		return e
	}
	name := filepath.Base(path)
	return b.UploadBytes(ctx, room, name, mime.TypeByExtension(filepath.Ext(name)), data)
}

// UploadBytes queues an in-memory attachment using the same upload path as files.
// It takes ownership of data and may encrypt it in place.
func (b *Backend) UploadBytes(ctx context.Context, room, name, mimeType string, data []byte) error {
	if len(data) > MaxAttachment {
		return errors.New("attachment exceeds 64 MiB")
	}
	r, e := b.Store.Room(room)
	if e != nil {
		return e
	}
	if r.Membership != "join" {
		return errors.New("join the room before uploading")
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	c := event.MessageEventContent{MsgType: event.MsgFile, Body: name, FileName: name, Info: &event.FileInfo{MimeType: mimeType, Size: len(data)}}
	if strings.HasPrefix(mimeType, "image/") {
		c.MsgType = event.MsgImage
		if dimensions, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			c.Info.Width = dimensions.Width
			c.Info.Height = dimensions.Height
		}
	}
	if strings.HasPrefix(mimeType, "audio/") {
		c.MsgType = event.MsgAudio
	}
	if strings.HasPrefix(mimeType, "video/") {
		c.MsgType = event.MsgVideo
	}
	uploadType := mimeType
	uploadName := name
	if r.Encrypted {
		ef := attachment.NewEncryptedFile()
		ef.EncryptInPlace(data)
		c.File = &event.EncryptedFileInfo{EncryptedFile: *ef}
		uploadType = "application/octet-stream"
		uploadName = ""
	}
	resp, e := b.Client.UploadMedia(ctx, mautrix.ReqUploadMedia{ContentBytes: data, ContentType: uploadType, FileName: uploadName})
	if e != nil {
		return e
	}
	if c.File != nil {
		c.File.URL = resp.ContentURI.CUString()
	} else {
		c.URL = resp.ContentURI.CUString()
	}
	return b.Queue(room, c)
}

// Image embeds existing Matrix media without downloading or uploading it again.
func (b *Backend) Image(room, source, caption string) error {
	uri, err := id.ContentURIString(source).Parse()
	if err != nil || uri.Homeserver == "" || uri.FileID == "" {
		return errors.New("image source must be a Matrix mxc:// URL; use /upload for local files")
	}
	if caption == "" {
		caption = "Image"
	}
	return b.Queue(room, event.MessageEventContent{MsgType: event.MsgImage, Body: caption, URL: uri.CUString()})
}

// Embed reuses image content, including the keys needed to decrypt encrypted media.
func (b *Backend) Embed(room, target, caption string) error {
	m, err := b.Store.Message(target)
	if err != nil {
		return err
	}
	if m.Redacted {
		return errors.New("message was deleted")
	}
	var evt event.Event
	if err = json.Unmarshal(m.Raw, &evt); err != nil {
		return err
	}
	if err = evt.Content.ParseRaw(evt.Type); err != nil {
		return err
	}
	original := evt.Content.AsMessage()
	if original.MsgType != event.MsgImage || (original.URL == "" && (original.File == nil || original.File.URL == "")) {
		return errors.New("message has no image to embed")
	}
	c := event.MessageEventContent{MsgType: event.MsgImage, Body: original.Body, FileName: original.FileName, URL: original.URL, File: original.File, Info: original.Info}
	if caption != "" {
		c.Body = caption
	}
	return b.Queue(room, c)
}

// ImagePreview downloads the original image or its attached thumbnail within limit.
func (b *Backend) ImagePreview(ctx context.Context, target string, limit int64, thumbnail bool) ([]byte, error) {
	c, err := b.attachmentContent(target)
	if err != nil {
		return nil, err
	}
	if c.MsgType != event.MsgImage {
		return nil, errors.New("message is not an image")
	}
	if !thumbnail {
		if c.Info != nil && int64(c.Info.Size) > limit {
			return nil, fmt.Errorf("attachment exceeds %d MiB", limit>>20)
		}
		return b.downloadMedia(ctx, c.URL, c.File, limit)
	}
	if c.Info == nil || (c.Info.ThumbnailFile == nil && (c.File != nil || c.Info.ThumbnailURL == "")) {
		return nil, errors.New("image has no thumbnail")
	}
	return b.downloadMedia(ctx, c.Info.ThumbnailURL, c.Info.ThumbnailFile, limit)
}

func (b *Backend) attachmentContent(target string) (*event.MessageEventContent, error) {
	m, err := b.Store.Message(target)
	if err != nil {
		return nil, err
	}
	if m.Redacted {
		return nil, errors.New("message was deleted")
	}
	var evt event.Event
	if err := json.Unmarshal(m.Raw, &evt); err != nil {
		return nil, err
	}
	if err := evt.Content.ParseRaw(evt.Type); err != nil {
		return nil, err
	}
	return evt.Content.AsMessage(), nil
}

func (b *Backend) downloadMedia(ctx context.Context, uri id.ContentURIString, encrypted *event.EncryptedFileInfo, limit int64) ([]byte, error) {
	if encrypted != nil {
		uri = encrypted.URL
	}
	if uri == "" {
		return nil, errors.New("message has no attachment")
	}
	parsed, e := uri.Parse()
	if e != nil {
		return nil, e
	}
	resp, e := b.Client.Download(ctx, parsed)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("attachment exceeds %d MiB", limit>>20)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("attachment exceeds %d MiB", limit>>20)
	}
	if encrypted != nil {
		if e = encrypted.DecryptInPlace(data); e != nil {
			return nil, fmt.Errorf("attachment integrity check: %w", e)
		}
	}
	return data, nil
}

func (b *Backend) Download(ctx context.Context, target, path string) error {
	content, e := b.attachmentContent(target)
	if e != nil {
		return e
	}
	data, e := b.downloadMedia(ctx, content.URL, content.File, MaxAttachment)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	closeErr := f.Close()
	if e != nil {
		os.Remove(path)
		return e
	}
	return closeErr
}

// PinRoom updates the user's Matrix favourite tag, shared across clients.
func (b *Backend) PinRoom(ctx context.Context, room string, pinned bool) error {
	var err error
	if pinned {
		err = b.Client.AddTag(ctx, id.RoomID(room), event.RoomTagFavourite, 0.5)
	} else {
		err = b.Client.RemoveTag(ctx, id.RoomID(room), event.RoomTagFavourite)
	}
	if err != nil {
		return err
	}
	if err = b.Store.PinRoom(room, pinned); err != nil {
		return err
	}
	b.Changed()
	return nil
}

// SyncRoomTags loads current room preferences without waiting for message history or
// decryption. Its sync token must never replace the main timeline sync token.
func (b *Backend) SyncRoomTags(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	const filter = `{"account_data":{"types":["m.push_rules"]},"presence":{"not_types":["*"]},"room":{"account_data":{"types":["m.tag"]},"state":{"not_types":["*"]},"timeline":{"not_types":["*"]},"ephemeral":{"not_types":["*"]}}}`
	res, err := b.Client.FullSyncRequest(ctx, mautrix.ReqSync{FilterID: filter, SetPresence: event.PresenceOffline})
	if err != nil {
		return err
	}
	// Replace the cache atomically so favourites removed by another client while
	// Weft was closed are cleared even when no m.tag event is returned.
	tx, err := b.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM room_pins"); err != nil {
		return err
	}
	for room, data := range res.Rooms.Join {
		for _, evt := range data.AccountData.Events {
			if evt.Type.Type != event.AccountDataRoomTags.Type {
				continue
			}
			var content event.TagEventContent
			if err = json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
				return err
			}
			if _, ok := content.Tags[event.RoomTagFavourite]; !ok {
				continue
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO rooms(id) VALUES(?)", room.String()); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO room_pins(room_id) VALUES(?)", room.String()); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	b.updateAlertSettings(res, true)
	b.Changed()
	return nil
}
