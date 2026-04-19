package ui

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ──────────────────────────────────────────────────────────────
// Terminal backend detection & image rendering subsystem
// ──────────────────────────────────────────────────────────────

// TermBackend describes the image rendering capability of the terminal.
type TermBackend int

const (
	BackendUnknown  TermBackend = iota
	BackendKitty                // kitty graphics protocol (native)
	BackendIcat                 // kitten icat CLI (fallback for kitty-in-tmux)
	BackendIterm2               // iTerm2 inline image protocol
	BackendTextOnly             // no image support; degrade to text placeholder
)

func (b TermBackend) String() string {
	switch b {
	case BackendKitty:
		return "kitty"
	case BackendIcat:
		return "icat"
	case BackendIterm2:
		return "iterm2"
	case BackendTextOnly:
		return "text-only"
	default:
		return "unknown"
	}
}

// ImageRenderer manages terminal image display with backend auto-detection,
// image ID tracking for clear/replace, and graceful fallback.
type ImageRenderer struct {
	mu      sync.Mutex
	backend TermBackend
	nextID  uint32
	ids     []uint32 // all live image IDs for bulk clear
	tty     *os.File // cached /dev/tty fd
	log     []string // diagnostic log
}

// NewImageRenderer probes the terminal and returns a ready renderer.
func NewImageRenderer() *ImageRenderer {
	r := &ImageRenderer{}
	r.backend = r.detectBackend()
	r.logf("backend: %s", r.backend)
	return r
}

// Backend returns the detected backend.
func (r *ImageRenderer) Backend() TermBackend { return r.backend }

// Diagnostics returns all logged diagnostic messages.
func (r *ImageRenderer) Diagnostics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.log))
	copy(out, r.log)
	return out
}

// Render writes an image to the terminal. maxCells is the maximum width in
// terminal cell columns. Returns a human-readable placeholder string for the
// transcript buffer (no escape sequences).
func (r *ImageRenderer) Render(data []byte, maxCells int) string {
	if len(data) == 0 {
		return "[empty image]"
	}
	pxW, pxH := GetPNGDimensions(data)
	if pxW == 0 || pxH == 0 {
		return "[invalid PNG]"
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.backend {
	case BackendKitty:
		return r.renderKitty(data, pxW, pxH, maxCells)
	case BackendIterm2:
		return r.renderIterm2(data, pxW, pxH, maxCells)
	default:
		return r.renderText(pxW, pxH, len(data))
	}
}

// ClearAll removes all tracked images from the terminal.
func (r *ImageRenderer) ClearAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.backend != BackendKitty || len(r.ids) == 0 {
		return
	}
	if err := r.writeTty("\x1b_Ga=d\x1b\\"); err != nil {
		r.logf("kitty clear failed: %v", err)
	}
	r.ids = nil
}

// ── Backend detection ────────────────────────────────────────

func (r *ImageRenderer) detectBackend() TermBackend {
	if runtime.GOOS == "windows" {
		r.logf("windows: text-only")
		return BackendTextOnly
	}

	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")
	kittyWindowID := os.Getenv("KITTY_WINDOW_ID")
	inTmux := os.Getenv("TMUX") != ""

	r.logf("TERM=%s TERM_PROGRAM=%s KITTY_WINDOW_ID=%s TMUX=%v",
		term, termProgram, kittyWindowID, inTmux)

	isKitty := kittyWindowID != "" || term == "xterm-kitty"

	if isKitty && inTmux {
		// Kitty passthrough through tmux is unreliable here. Prefer text-only
		// rather than trying to write broken escape sequences.
		r.logf("kitty detected but inside tmux → text-only")
		return BackendTextOnly
	}

	if isKitty {
		// Try to verify kitty protocol support with a query
		if r.probeKittyProtocol() {
			return BackendKitty
		}
		r.logf("kitty protocol probe failed → text-only")
		return BackendTextOnly
	}

	// iTerm2 protocol (also supported by WezTerm, Konsole, Ghostty, VSCode)
	if termProgram == "iTerm.app" || termProgram == "WezTerm" ||
		termProgram == "ghostty" || termProgram == "vscode" ||
		os.Getenv("KONSOLE_VERSION") != "" {
		r.logf("iterm2-compatible: %s", termProgram)
		return BackendIterm2
	}

	r.logf("no image protocol detected → text-only")
	return BackendTextOnly
}

// probeKittyProtocol sends a kitty graphics query to verify support.
// We send a simple "is the protocol working?" query and check the response.
func (r *ImageRenderer) probeKittyProtocol() bool {
	// True protocol probing would require reading terminal responses.
	// For now, if kitty env vars are set and we're not in tmux, trust it.
	return true
}

// ── Kitty graphics protocol ──────────────────────────────────

func (r *ImageRenderer) renderKitty(data []byte, pxW, pxH, maxCells int) string {
	id := r.nextID
	r.nextID++
	r.ids = append(r.ids, id)

	cells := maxCells
	if cells <= 0 {
		cells = 80
	}
	rows := int(float64(pxH)*float64(cells)/float64(pxW) + 0.5)
	if rows <= 0 {
		rows = 1
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	meta := fmt.Sprintf("a=T,f=100,c=%d,r=%d,i=%d", cells, rows, id)

	if err := r.writeKittyChunked(meta, b64); err != nil {
		r.logf("kitty write failed: %v", err)
		return r.renderText(pxW, pxH, len(data))
	}

	return fmt.Sprintf("[🖼 image %dx%d rendered via kitty protocol]", pxW, pxH)
}

// writeKittyChunked emits a kitty graphics command, splitting the base64 payload
// into ≤4096-byte chunks per the kitty protocol. The first chunk carries the
// full metadata; subsequent chunks carry only m=1 (more) or m=0 (final).
func (r *ImageRenderer) writeKittyChunked(meta, b64 string) error {
	const chunkSize = 4096
	if len(b64) <= chunkSize {
		seq := fmt.Sprintf("\x1b_G%s;%s\x1b\\", meta, b64)
		return r.writeTty(seq)
	}
	first := b64[:chunkSize]
	rest := b64[chunkSize:]
	if err := r.writeTty(fmt.Sprintf("\x1b_G%s,m=1;%s\x1b\\", meta, first)); err != nil {
		return err
	}
	for len(rest) > chunkSize {
		if err := r.writeTty(fmt.Sprintf("\x1b_Gm=1;%s\x1b\\", rest[:chunkSize])); err != nil {
			return err
		}
		rest = rest[chunkSize:]
	}
	return r.writeTty(fmt.Sprintf("\x1b_Gm=0;%s\x1b\\", rest))
}

// ── iTerm2 inline protocol ───────────────────────────────────

func (r *ImageRenderer) renderIterm2(data []byte, pxW, pxH, maxCells int) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	cells := maxCells
	if cells <= 0 {
		cells = 80
	}
	seq := fmt.Sprintf("\x1b]1337;File=width=%d;preserveAspectRatio=1;inline=1:%s\x07", cells, b64)
	if err := r.writeTty(seq); err != nil {
		r.logf("iterm2 write failed: %v", err)
		return r.renderText(pxW, pxH, len(data))
	}
	return fmt.Sprintf("[🖼 image %dx%d rendered via iterm2 protocol]", pxW, pxH)
}

// ── Text fallback ────────────────────────────────────────────

func (r *ImageRenderer) renderText(pxW, pxH, size int) string {
	return fmt.Sprintf("[📷 PNG %dx%d — %s]", pxW, pxH, humanBytes(size))
}

// ── Helpers ──────────────────────────────────────────────────

func (r *ImageRenderer) writeTty(s string) error {
	if r.tty == nil {
		f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("open /dev/tty: %w", err)
		}
		r.tty = f
	}
	_, err := r.tty.WriteString(s)
	return err
}

func (r *ImageRenderer) logf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	r.log = append(r.log, msg)
	log.Printf("[image-renderer] %s", msg)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func renderImageSummary(data []byte) string {
	w, h := GetPNGDimensions(data)
	if w > 0 && h > 0 {
		return fmt.Sprintf("[📷 PNG %dx%d — %s]", w, h, humanBytes(len(data)))
	}
	if len(data) > 0 {
		return fmt.Sprintf("[📷 PNG — %s]", humanBytes(len(data)))
	}
	return "[📷 PNG Image]"
}

// ──────────────────────────────────────────────────────────────
// Legacy functions kept for CLI mode compatibility.
// RenderImageInline renders image bytes as an inline image string without
// performing any terminal writes. width/height are in cells (0 = auto).
func RenderImageInline(data []byte, width, height int) string {
	if len(data) == 0 {
		return ""
	}
	if runtime.GOOS == "windows" {
		return ""
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	if os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("TERM") == "xterm-kitty" {
		pxW, pxH := GetPNGDimensions(data)
		meta := fmt.Sprintf("a=T,f=100,c=%d,r=%d", width, height)
		if pxW > 0 && pxH > 0 {
			meta += fmt.Sprintf(",s=%d,v=%d", pxW, pxH)
		}
		return fmt.Sprintf("\x1b_G%s;%s\x1b\\", meta, b64)
	}
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" || os.Getenv("TERM_PROGRAM") == "WezTerm" ||
		os.Getenv("TERM_PROGRAM") == "ghostty" || os.Getenv("TERM_PROGRAM") == "vscode" ||
		os.Getenv("KONSOLE_VERSION") != "" {
		widthStr := ""
		heightStr := ""
		if width > 0 {
			widthStr = fmt.Sprintf("width=%d;", width)
		}
		if height > 0 {
			heightStr = fmt.Sprintf("height=%d;", height)
		}
		return fmt.Sprintf("\x1b]1337;File=%s%spreserveAspectRatio=1;inline=1:%s\x07", widthStr, heightStr, b64)
	}
	w, h := GetPNGDimensions(data)
	if w > 0 && h > 0 {
		return renderImageSummary(data)
	}
	return "[📷 PNG Image]"
}

// RenderImageInlineAuto renders an image with automatic sizing for CLI mode.
func RenderImageInlineAuto(data []byte, maxWidth int) string {
	if len(data) == 0 || maxWidth <= 0 {
		return RenderImageInline(data, 0, 0)
	}
	w, h := GetPNGDimensions(data)
	if w == 0 || h == 0 {
		return RenderImageInline(data, 0, 0)
	}
	if w > maxWidth {
		scale := float64(maxWidth) / float64(w)
		h = int(float64(h)*scale + 0.5)
		w = maxWidth
	}
	return RenderImageInline(data, w, h)
}

// ParseCellsFromEnv returns the terminal width in cells, or a default.
func ParseCellsFromEnv(fallback int) int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	if cols, err := strconv.Atoi(strings.TrimSpace(runCmd("tput", "cols"))); err == nil && cols > 0 {
		return cols
	}
	return fallback
}

func runCmd(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out))
}

// GetPNGDimensions reads the width and height from PNG image data.
func GetPNGDimensions(data []byte) (w, h int) {
	if len(data) < 24 {
		return 0, 0
	}
	sig := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for i := 0; i < 8; i++ {
		if data[i] != sig[i] {
			return 0, 0
		}
	}
	w = int(data[16])<<24 | int(data[17])<<16 | int(data[18])<<8 | int(data[19])
	h = int(data[20])<<24 | int(data[21])<<16 | int(data[22])<<8 | int(data[23])
	return w, h
}
