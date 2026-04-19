package ui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// DefaultCellAspect is the width/height ratio of a terminal cell.
// Typical monospace fonts have cells ~10px wide × ~20px tall = 0.5.
var DefaultCellAspect = 0.5

// RenderHalfBlocks decodes a PNG/JPEG and renders it as a string of ANSI
// half-block characters (▀) — each cell encodes two vertical pixels via
// foreground (top) and background (bottom) truecolor codes. The result is
// plain text that flows inside the transcript pane: it scrolls, clips, and
// disappears with the buffer like any other content.
//
// maxCols and maxRows are both in terminal cells. The image is fit to the
// smaller of the two constraints, preserving aspect ratio.
// cellAspect is the width/height ratio of a terminal cell (e.g. 0.5 for 10×20px).
func RenderHalfBlocks(data []byte, maxCols, maxRows int, cellAspect float64) string {
	if len(data) == 0 || maxCols <= 0 || maxRows <= 0 {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return ""
	}

	if cellAspect <= 0 {
		cellAspect = DefaultCellAspect
	}

	// Each cell row displays 2 source pixel rows via half-block chars.
	// cellHeight in units where width=1: cellHeight = 1/cellAspect
	// Visual height of one cell row = 2 pixels × cellHeight
	// For correct aspect: srcW/srcH = targetCols / (targetRows × 2 × cellHeight)
	cellHeight := 1.0 / cellAspect
	pixelsPerCellRow := 2.0 * cellHeight

	targetCols := maxCols
	targetRows := int(float64(srcH)*float64(targetCols)/(float64(srcW)*pixelsPerCellRow) + 0.5)
	if targetRows > maxRows {
		targetRows = maxRows
		targetCols = int(float64(srcW)*float64(targetRows)*pixelsPerCellRow/float64(srcH) + 0.5)
	}
	if targetCols <= 0 {
		targetCols = 1
	}
	if targetRows <= 0 {
		targetRows = 1
	}

	// Each cell row consumes 2 source rows. Use nearest-neighbor sampling.
	pxRows := targetRows * 2
	var b strings.Builder
	b.Grow(targetCols * targetRows * 24)

	for cy := 0; cy < targetRows; cy++ {
		topY := bounds.Min.Y + (cy*2)*srcH/pxRows
		botY := bounds.Min.Y + (cy*2+1)*srcH/pxRows
		if botY >= bounds.Max.Y {
			botY = bounds.Max.Y - 1
		}
		var lastTop, lastBot uint32 = 0xffffffff, 0xffffffff
		for cx := 0; cx < targetCols; cx++ {
			sx := bounds.Min.X + cx*srcW/targetCols
			tr, tg, tb := rgb(img, sx, topY)
			br, bg, bb := rgb(img, sx, botY)
			topKey := uint32(tr)<<16 | uint32(tg)<<8 | uint32(tb)
			botKey := uint32(br)<<16 | uint32(bg)<<8 | uint32(bb)
			if topKey != lastTop {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", tr, tg, tb)
				lastTop = topKey
			}
			if botKey != lastBot {
				fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm", br, bg, bb)
				lastBot = botKey
			}
			b.WriteString("▀")
		}
		b.WriteString("\x1b[0m")
		if cy < targetRows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func rgb(img image.Image, x, y int) (r, g, bl uint8) {
	cr, cg, cb, _ := img.At(x, y).RGBA()
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8)
}
