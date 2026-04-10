package ui

import (
	"encoding/base64"
	"fmt"
	"runtime"
)

// RenderImageInline renders image bytes as an iTerm2 inline image.
// width/height are in cells (0 = auto). Returns empty string if the terminal
// does not support inline images.
func RenderImageInline(data []byte, width, height int) string {
	if len(data) == 0 {
		return ""
	}
	// Only supported on non-Windows (iTerm2, Konsole, etc.)
	if runtime.GOOS == "windows" {
		return ""
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	widthStr := ""
	heightStr := ""
	if width > 0 {
		widthStr = fmt.Sprintf("width=%d;", width)
	}
	if height > 0 {
		heightStr = fmt.Sprintf("height=%d;", height)
	}
	// iTerm2 inline image: ESC ]1337;File=...[metadata];inline=1:BASE64... BEL
	return fmt.Sprintf("\x1b]1337;File=%s%sinline=1:%s\x07", widthStr, heightStr, b64)
}

// RenderImageInlineAuto renders an image with automatic sizing based on
// dimensions of the image data. It scales down to fit within maxWidth cells.
func RenderImageInlineAuto(data []byte, maxWidth int) string {
	if len(data) == 0 || maxWidth <= 0 {
		return RenderImageInline(data, 0, 0)
	}
	w, h := GetPNGDimensions(data)
	if w == 0 || h == 0 {
		return RenderImageInline(data, 0, 0)
	}
	// Scale to maxWidth cells, preserving aspect ratio.
	// Approximate: cells ≈ 2× the pixel-to-cell ratio for typical fonts.
	// We use a conservative approach: scale so width fits maxWidth.
	if w > maxWidth {
		scale := float64(maxWidth) / float64(w)
		h = int(float64(h)*scale + 0.5)
		w = maxWidth
	}
	return RenderImageInline(data, w, h)
}

// GetPNGDimensions reads the width and height from PNG image data.
// Returns (0,0) if the data is not a valid PNG.
func GetPNGDimensions(data []byte) (w, h int) {
	if len(data) < 24 {
		return 0, 0
	}
	// PNG signature check
	sig := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for i := 0; i < 8; i++ {
		if data[i] != sig[i] {
			return 0, 0
		}
	}
	// IHDR chunk at offset 8: width (4 bytes big-endian), height (4 bytes big-endian)
	w = int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	h = int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	return w, h
}
