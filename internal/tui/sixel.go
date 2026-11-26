package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/sixel"
	"golang.org/x/image/draw"
)

// Run keeps terminal image painting synchronized with Bubble Tea's text frames.
func Run(ctx context.Context, m Model) (tea.Model, error) {
	options := []tea.ProgramOption{tea.WithContext(ctx), tea.WithFPS(120)}
	if m.settings.ImagePreviews {
		m.graphics = &sixelOutput{File: os.Stdout}
		options = append(options, tea.WithOutput(m.graphics))
	}
	return tea.NewProgram(m, options...).Run()
}

type sixelTile struct {
	image                 *image.NRGBA
	width, height         int
	cellWidth, cellHeight int
}

func (tile sixelTile) rows() int {
	return (tile.height + tile.cellHeight - 1) / tile.cellHeight
}

func (tile sixelTile) columns() int {
	return (tile.width + tile.cellWidth - 1) / tile.cellWidth
}

type sixelPlacement struct {
	tile                 sixelTile
	x, y, firstRow, rows int
}

type sixelFrame struct {
	id     uint64
	images []sixelPlacement
}

type sixelOutput struct {
	*os.File
	mu      sync.Mutex
	pending []sixelFrame
	active  []sixelPlacement
	frameID uint64
	encoded map[sixelCrop]string
}

type sixelCrop struct {
	tile           sixelTile
	firstRow, rows int
}

const graphicsTitle = "weft:graphics:"
const graphicsTitleSequence = "\x1b]2;" + graphicsTitle

// Bubble Tea emits a changed window title in the same write as its text frame.
// Use that sequence as a frame marker and restore the normal title on output.
func (o *sixelOutput) frameTitle(images []sixelPlacement) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) == 0 && len(images) == 0 {
		return "weft"
	}
	if len(o.pending) == 0 || !slices.Equal(o.pending[len(o.pending)-1].images, images) {
		o.frameID++
		if len(o.pending) == 16 {
			o.pending = slices.Delete(o.pending, 0, 1)
		}
		o.pending = append(o.pending, sixelFrame{o.frameID, images})
	}
	return graphicsTitle + strconv.FormatUint(o.frameID, 10)
}

func (o *sixelOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	inputLength := len(p)
	var frame bytes.Buffer
	// Never draw into the normal screen on shutdown or suspension.
	leaving := bytes.Contains(p, []byte(ansi.ResetModeAltScreenSaveCursor))
	if leaving {
		o.active = nil
	}
	marker := bytes.Index(p, []byte(graphicsTitleSequence))
	if marker < 0 && len(o.active) == 0 {
		return o.File.Write(p)
	}
	if i := marker; i >= 0 {
		if end := bytes.IndexByte(p[i:], '\a'); end >= 0 {
			id, _ := strconv.ParseUint(string(p[i+len(graphicsTitleSequence):i+end]), 10, 64)
			for _, next := range o.pending {
				if !leaving && next.id == id {
					if !slices.Equal(o.active, next.images) {
						clearSixels(&frame, o.active)
						o.active = next.images
					}
					break
				}
			}
			frame.Write(p[:i])
			frame.WriteString(ansi.SetWindowTitle("weft"))
			p = p[i+end+1:]
		}
	}
	frame.Write(p)
	if len(o.active) != 0 {
		// Paint after the text so printing the cells under an image cannot erase it.
		var images bytes.Buffer
		images.WriteString(ansi.SaveCursor)
		for _, placement := range o.active {
			images.WriteString(ansi.CursorPosition(placement.x+1, placement.y+1))
			images.WriteString(o.sixel(placement))
		}
		images.WriteString(ansi.RestoreCursor)
		if end := bytes.LastIndex(frame.Bytes(), []byte(ansi.ResetModeSynchronizedOutput)); end >= 0 {
			tail := bytes.Clone(frame.Bytes()[end:])
			frame.Truncate(end)
			frame.Write(images.Bytes())
			frame.Write(tail)
		} else {
			frame.Write(images.Bytes())
		}
	}
	_, err := o.File.Write(frame.Bytes())
	if err != nil {
		return 0, err
	}
	return inputLength, nil
}

func clearSixels(out *bytes.Buffer, images []sixelPlacement) {
	if len(images) == 0 {
		return
	}
	out.WriteString(ansi.SaveCursor)
	out.WriteString("\x1b[0m")
	for _, p := range images {
		for y := p.y; y < p.y+p.rows; y++ {
			out.WriteString(ansi.CursorPosition(p.x+1, y+1))
			fmt.Fprintf(out, "\x1b[%dX", p.tile.columns())
		}
	}
	out.WriteString(ansi.RestoreCursor)
}

func (o *sixelOutput) sixel(p sixelPlacement) string {
	key := sixelCrop{p.tile, p.firstRow, p.rows}
	if encoded, ok := o.encoded[key]; ok {
		return encoded
	}
	tile := p.tile
	start := p.firstRow * tile.cellHeight
	height := min(tile.height-start, p.rows*tile.cellHeight)
	scaled := tile.image
	if tile.width != scaled.Bounds().Dx() || tile.height != scaled.Bounds().Dy() {
		scaled = image.NewNRGBA(image.Rect(0, 0, tile.width, tile.height))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), tile.image, tile.image.Bounds(), draw.Src, nil)
	}
	src := scaled.SubImage(image.Rect(0, start, tile.width, start+height))
	img := image.NewNRGBA(image.Rect(0, 0, tile.width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(previewBackground), image.Point{}, draw.Src)
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Over)
	var payload bytes.Buffer
	if err := (&sixel.Encoder{}).Encode(&payload, img); err != nil {
		return ""
	}
	encoded := ansi.SixelGraphics(0, 1, 0, payload.Bytes())
	if len(o.encoded) >= 64 || o.encoded == nil {
		o.encoded = make(map[sixelCrop]string)
	}
	o.encoded[key] = encoded
	return encoded
}

func (m Model) imageTile(thumb *image.NRGBA, maxColumns, maxRows int) sixelTile {
	if thumb == nil || !m.sixelSupported || m.cellWidth <= 0 || m.cellHeight <= 0 || maxRows <= 0 || maxColumns <= 0 {
		return sixelTile{}
	}
	bounds := thumb.Bounds()
	width, height := previewSize(bounds.Dx(), bounds.Dy(), min(bounds.Dx(), maxColumns*m.cellWidth), min(bounds.Dy(), maxRows*m.cellHeight))
	return sixelTile{thumb, width, height, m.cellWidth, m.cellHeight}
}

func (tile sixelTile) blankLines() []string {
	rows := make([]string, tile.rows())
	blank := "    " + strings.Repeat(" ", tile.columns())
	for i := range rows {
		rows[i] = blank
	}
	return rows
}
