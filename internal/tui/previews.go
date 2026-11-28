package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/image/draw"
	"weft/internal/store"
)

const (
	previewBytes      = 8 << 20
	previewPixels     = 16 << 20
	previewWidth      = 48
	previewRows       = 8
	previewCacheSize  = 48
	maxPreviewWidth   = 640
	maxPreviewHeight  = 360
	maxImageDimension = 8192
)

type imagePreviewMsg struct {
	eventID    string
	attachment *stagedAttachment
	image      *image.NRGBA
}

// decodePreview bounds both the decoded source and the stored thumbnail.
func decodePreview(data []byte) *image.NRGBA {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 ||
		config.Width > maxImageDimension || config.Height > maxImageDimension ||
		config.Width > previewPixels/config.Height {
		return nil
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	bounds := source.Bounds()
	if bounds.Empty() {
		return nil
	}
	width, height := previewSize(bounds.Dx(), bounds.Dy(), min(bounds.Dx(), maxPreviewWidth), min(bounds.Dy(), maxPreviewHeight))
	thumb := image.NewNRGBA(image.Rect(0, 0, width, height))
	if width == bounds.Dx() && height == bounds.Dy() {
		draw.Draw(thumb, thumb.Bounds(), source, bounds.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(thumb, thumb.Bounds(), source, bounds, draw.Src, nil)
	}
	return thumb
}

func previewSize(width, height, maxWidth, maxHeight int) (int, int) {
	scale := min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height))
	return max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
}

var previewBackground = color.NRGBAModel.Convert(bg).(color.NRGBA)

func previewPixel(c color.NRGBA) color.NRGBA {
	a := int(c.A)
	return color.NRGBA{
		R: uint8((int(c.R)*a + int(previewBackground.R)*(255-a)) / 255),
		G: uint8((int(c.G)*a + int(previewBackground.G)*(255-a)) / 255),
		B: uint8((int(c.B)*a + int(previewBackground.B)*(255-a)) / 255), A: 255,
	}
}

func previewLines(thumb *image.NRGBA, maxWidth, maxRows int) []string {
	if thumb == nil || maxWidth < 1 || maxRows < 1 {
		return nil
	}
	width, height := previewSize(thumb.Bounds().Dx(), thumb.Bounds().Dy(), min(maxWidth, previewWidth), 2*maxRows)
	return previewLinesSized(thumb, width, height)
}

func previewLinesSized(thumb *image.NRGBA, width, height int) []string {
	at := func(x, y int) color.NRGBA {
		return previewPixel(thumb.NRGBAAt(x*thumb.Bounds().Dx()/width, y*thumb.Bounds().Dy()/height))
	}
	rows := make([]string, 0, (height+1)/2)
	for y := 0; y < height; y += 2 {
		var row strings.Builder
		row.WriteString("    ")
		for x := range width {
			top := at(x, y)
			bottom := previewBackground
			if y+1 < height {
				bottom = at(x, y+1)
			}
			fmt.Fprintf(&row, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}
		row.WriteString("\x1b[0m")
		rows = append(rows, row.String())
	}
	return rows
}

// timelinePreview reserves a box from the event, independent of the download.
// Missing dimensions use a bounded box; mismatched thumbnails fit inside it.
func (m Model) timelinePreview(msg store.Message, columns int) ([]string, sixelTile) {
	cellWidth, cellHeight := 1, 2
	maxWidth, maxHeight := min(columns, previewWidth), 2*previewRows
	sixel := m.sixelSupported && m.cellWidth > 0 && m.cellHeight > 0
	if sixel {
		cellWidth, cellHeight = m.cellWidth, m.cellHeight
		maxWidth = min(maxPreviewWidth, columns*cellWidth)
		maxHeight = min(maxPreviewHeight, min(17, max(1, m.height-13))*cellHeight)
	}
	width, height := maxWidth, maxHeight
	info := msg.ImageInfo
	if info != nil && (info.Width <= 0 || info.Height <= 0) {
		info = info.ThumbnailInfo
	}
	if info != nil && info.Width > 0 && info.Height > 0 {
		if sixel {
			maxWidth = min(maxWidth, info.Width)
			maxHeight = min(maxHeight, info.Height)
		}
		width, height = previewSize(info.Width, info.Height, maxWidth, maxHeight)
	}
	rows := (height + cellHeight - 1) / cellHeight
	cols := (width + cellWidth - 1) / cellWidth
	thumb, loaded := m.previews[msg.ID]
	lines := make([]string, 0, rows)
	var tile sixelTile
	if thumb != nil {
		bounds := thumb.Bounds()
		w, h := previewSize(bounds.Dx(), bounds.Dy(), width, height)
		if sixel {
			tile = sixelTile{thumb, w, h, cellWidth, cellHeight}
			lines = append(lines, tile.blankLines()...)
		} else {
			lines = append(lines, previewLinesSized(thumb, w, h)...)
		}
	}
	if thumb == nil {
		label := "Loading image…"
		if msg.Pending {
			label = "Image preview"
		} else if loaded {
			label = "Preview unavailable"
		}
		style := lipgloss.NewStyle().Background(panel).Foreground(muted)
		for len(lines) < rows {
			text := ""
			if len(lines) == (rows-1)/2 {
				label = clip(label, cols)
				text = strings.Repeat(" ", (cols-lipgloss.Width(label))/2) + label
			}
			lines = append(lines, "    "+style.Render(fit(text, cols)))
		}
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines, tile
}

func (m *Model) nextImagePreview() tea.Cmd {
	if !m.settings.ImagePreviews || m.backend == nil || m.mode != "" ||
		m.previewing != "" || m.width < 36 || m.height < 14 {
		return nil
	}
	start, end := m.visibleTimeline()
	visibleImages := 0
	for i := len(m.messages) - 1; i >= 0 && visibleImages < previewCacheSize; i-- {
		msg := m.messages[i]
		span := m.spans[msg.ID]
		if span[1] <= start {
			break
		}
		if span[0] >= end || msg.Kind != "m.image" || msg.Redacted || msg.Pending {
			continue
		}
		visibleImages++
		if _, loaded := m.previews[msg.ID]; loaded {
			continue
		}
		m.previewing = msg.ID
		backend, ctx := m.backend, m.ctx
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			result := imagePreviewMsg{eventID: msg.ID}
			for _, thumbnail := range []bool{false, true} {
				if data, err := backend.ImagePreview(ctx, msg.ID, previewBytes, thumbnail); err == nil {
					result.image = decodePreview(data)
					if result.image != nil {
						break
					}
				}
			}
			return result
		}
	}
	return nil
}

func (m *Model) previewAttachment() tea.Cmd {
	if !m.settings.ImagePreviews {
		return nil
	}
	attachment := m.attachment
	var data []byte
	if attachment.path == "" {
		if len(attachment.data) > previewBytes {
			return nil
		}
		// Sending can encrypt the attachment in place while the preview is decoding.
		data = slices.Clone(attachment.data)
	}
	return func() tea.Msg {
		result := imagePreviewMsg{attachment: attachment}
		if attachment.path == "" {
			result.image = decodePreview(data)
		} else if file, err := os.Open(attachment.path); err == nil {
			defer file.Close()
			if info, err := file.Stat(); err == nil && info.Mode().IsRegular() && info.Size() <= previewBytes {
				if data, err := io.ReadAll(io.LimitReader(file, previewBytes+1)); err == nil && len(data) <= previewBytes {
					result.image = decodePreview(data)
				}
			}
		}
		return result
	}
}

func (m Model) composerPreviewRows() int {
	return min(14, max(0, (m.height-14)/2, m.height-18))
}

func (m Model) composerTile() sixelTile {
	return m.imageTile(m.attachmentPreview, m.mainWidth()-6, m.composerPreviewRows())
}

func (m Model) composerPreviewHeight() int {
	if tile := m.composerTile(); tile.image != nil {
		return tile.rows()
	}
	if m.attachmentPreview == nil || m.composerPreviewRows() == 0 {
		return 0
	}
	bounds := m.attachmentPreview.Bounds()
	_, height := previewSize(bounds.Dx(), bounds.Dy(), min(previewWidth, m.mainWidth()-6), 2*m.composerPreviewRows())
	return (height + 1) / 2
}

func (m Model) composerPreview() []string {
	return previewLines(m.attachmentPreview, m.mainWidth()-6, m.composerPreviewRows())
}
