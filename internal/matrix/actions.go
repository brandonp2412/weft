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

const maxAttachment = 64 << 20

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
	if info.Size() > maxAttachment {
		return errors.New("attachment exceeds 64 MiB")
	}
	data, e := io.ReadAll(io.LimitReader(f, maxAttachment+1))
	if e != nil {
		return e
	}
	if len(data) > maxAttachment {
		return errors.New("attachment exceeds 64 MiB")
	}
	r, e := b.Store.Room(room)
	if e != nil {
		return e
	}
	if r.Membership != "join" {
		return errors.New("join the room before uploading")
	}
	name := filepath.Base(path)
	mimeType := mime.TypeByExtension(filepath.Ext(name))
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

func (b *Backend) Download(ctx context.Context, target, path string) error {
	m, e := b.Store.Message(target)
	if e != nil {
		return e
	}
	if m.Redacted {
		return errors.New("message was deleted")
	}
	var evt event.Event
	if e = json.Unmarshal(m.Raw, &evt); e != nil {
		return e
	}
	if e = evt.Content.ParseRaw(evt.Type); e != nil {
		return e
	}
	c := evt.Content.AsMessage()
	uri := c.URL
	if c.File != nil {
		uri = c.File.URL
	}
	if uri == "" {
		return errors.New("message has no attachment")
	}
	parsed, e := uri.Parse()
	if e != nil {
		return e
	}
	resp, e := b.Client.Download(ctx, parsed)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxAttachment {
		return errors.New("download exceeds 64 MiB")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxAttachment+1))
	if e != nil {
		return e
	}
	if len(data) > maxAttachment {
		return errors.New("download exceeds 64 MiB")
	}
	if c.File != nil {
		if e = c.File.DecryptInPlace(data); e != nil {
			return fmt.Errorf("attachment integrity check: %w", e)
		}
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
