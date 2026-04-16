package wrap

import (
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	// DefaultTerminalWidth is used when we can't detect terminal size.
	DefaultTerminalWidth = 80
	// TabWidth is the standard terminal tab stop width.
	TabWidth = 8
	// Scale is the fixed-point scale for width calculations.
	Scale = 1 << 16 // 16-bit fixed point for sub-pixel precision
)

// TermWidth returns the current terminal width in characters.
// Falls back to COLUMNS env var, then DefaultTerminalWidth.
func TermWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		if c := os.Getenv("COLUMNS"); c != "" {
			if v, err := strconv.Atoi(c); err == nil && v > 0 {
				return v
			}
		}
		return DefaultTerminalWidth
	}
	return w
}

// RuneWidth returns the display width of a rune in a terminal.
// Handles emoji, CJK, zero-width joiners, and combining chars.
func RuneWidth(r rune) int {
	switch {
	// Control characters
	case r < 0x20 || r == 0x7f:
		return 0

	// Combining characters that don't advance cursor
	case r >= 0x300 && r <= 0x36f: // combining diacritical marks
		return 0
	case r >= 0x1160 && r <= 0x11ff: // Hangul jamo
		return 0
	case r >= 0x17b0 && r <= 0x17ff: // Khmer
		return 0
	case r >= 0x200b && r <= 0x200f: // zero width chars
		return 0
	case r >= 0x2028 && r <= 0x202e: // specials
		return 0
	case r >= 0xfe00 && r <= 0xfe0f: // variation selectors
		return 0
	case r >= 0x1f3fb && r <= 0x1f3ff: // Fitzpatrick modifiers
		return 0

	// Zero-width joiners (part of emoji sequences)
	case r == 0x200d: // ZWJ
		return 0

	// East Asian Width handling
	case r >= 0x1100 && r <= 0x115f: // Hangul
		return 2
	case r >= 0x2e80 && r <= 0x2eff: // CJK Radicals Supplement
		return 2
	case r >= 0x2f00 && r <= 0x2fdf: // Kangxi Radicals
		return 2
	case r >= 0x2ff0 && r <= 0x2fff: // CJK Symbols and Punctuation (partial)
		return 2
	case r >= 0x3000 && r <= 0x303f: // CJK Symbols and Punctuation
		return 2
	case r >= 0x3040 && r <= 0x9fff: // CJK Unified Ideographs
		return 2
	case r >= 0xa000 && r <= 0xa49f: // Yi Syllables
		return 2
	case r >= 0xa4d0 && r <= 0xa4ff: // Yi Radicals
		return 2
	case r >= 0xac00 && r <= 0xd7a3: // Hangul Syllables
		return 2
	case r >= 0xf900 && r <= 0xfaff: // CJK Compatibility Ideographs
		return 2
	case r >= 0xfe10 && r <= 0xfe1f: // Vertical forms
		return 2
	case r >= 0xfe30 && r <= 0xfe6f: // CJK Compatibility Forms
		return 2
	case r >= 0xff00 && r <= 0xff60: // Halfwidth and Fullwidth Forms
		return 2
	case r >= 0xffe0 && r <= 0xffe6: // Fullwidth forms (more)
		return 2
	case r >= 0x20000 && r <= 0x2a6df: // CJK Extension B-E
		return 2
	case r >= 0x2a700 && r <= 0x2b73f: // CJK Extension F
		return 2
	case r >= 0x2b740 && r <= 0x2b81f: // CJK Extension G
		return 2
	case r >= 0x2b820 && r <= 0x2ceaf: // CJK Extension H
		return 2
	case r >= 0x2ceb0 && r <= 0x2ebef: // CJK Extension I
		return 2
	case r >= 0x30000 && r <= 0x3134f: // CJK Extension I (more)
		return 2

	// Emoji that take 2 columns
	case r >= 0x1f004 && r <= 0x1f9ff: // Miscellaneous Symbols and Pictographs + Emoji
		// Check for emoji modifiers that reduce width
		// Standard emoji are 2 columns
		return 2

	// Regional indicators (flags)
	case r >= 0x1f1e6 && r <= 0x1f1ff:
		return 2
	}

	// Most printable ASCII chars are 1 column
	return 1
}

// StringWidth returns the terminal display width of a string.
// Accounts for emoji, CJK, combining chars, and escape sequences.
func StringWidth(s string) int {
	if s == "" {
		return 0
	}
	w := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		// Skip ANSI escape sequences
		if r == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we hit a letter (end of CSI sequence)
			for j := i + 2; j < len(s); j++ {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					i = j + 1
					goto next
				}
			}
			// No end found, skip rest
			break
		}

		w += RuneWidth(r)
	next:
		i += size
	}
	return w
}

// WordBreak splits text on word boundaries, returning segments.
// Each segment can be safely wrapped at its natural break point.
func WordBreak(text string) []string {
	var words []string
	var current strings.Builder

	iter := NewSegmentIterator(text)
	for iter.Next() {
		seg := iter.Segment()
		if seg.IsBreak {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
			if seg.Width > 0 { // visible break (space)
				words = append(words, seg.Text)
			}
		} else {
			current.WriteString(seg.Text)
		}
	}

	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// SegmentIterator walks through text as displayable units.
type SegmentIterator struct {
	text  string
	pos   int
	seg   Segment
}

type Segment struct {
	Text     string
	Width    int // display columns
	IsBreak  bool
	RuneLen  int
}

// NewSegmentIterator creates an iterator over text segments.
func NewSegmentIterator(text string) *SegmentIterator {
	return &SegmentIterator{text: text}
}

// Next advances to the next segment.
func (s *SegmentIterator) Next() bool {
	if s.pos >= len(s.text) {
		return false
	}

	start := s.pos
	r, size := utf8.DecodeRuneInString(s.text[s.pos:])
	s.pos += size

	switch r {
	case '\n':
		s.seg = Segment{Text: "\n", Width: 0, IsBreak: true, RuneLen: 1}
	case '\t':
		s.seg = Segment{Text: "\t", Width: TabWidth, IsBreak: true, RuneLen: 1}
	case ' ', '\u00a0', '\u3000': // space, nbsp, ideographic space
		s.seg = Segment{Text: string(r), Width: 1, IsBreak: true, RuneLen: 1}
	case 0x200b: // ZWSP
		s.seg = Segment{Text: "", Width: 0, IsBreak: true, RuneLen: size}
	default:
		s.seg = Segment{
			Text:    s.text[start:s.pos],
			Width:   RuneWidth(r),
			IsBreak: false,
			RuneLen: 1,
		}
	}

	return true
}

// Segment returns the current segment.
func (s *SegmentIterator) Segment() Segment {
	return s.seg
}

// Wrap wraps text to fit within maxWidth columns.
// Prefers breaking at word boundaries, falls back to breaking mid-word.
func Wrap(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	if text == "" {
		return nil
	}

	var lines []string
	var current strings.Builder
	currentWidth := 0

	iter := NewSegmentIterator(text)
	for iter.Next() {
		seg := iter.Segment()

		if seg.IsBreak && seg.Text == "\n" {
			// Hard newline
			if current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
				currentWidth = 0
			}
			continue
		}

		// Would this segment fit?
		if currentWidth+seg.Width <= maxWidth {
			current.WriteString(seg.Text)
			currentWidth += seg.Width
		} else if currentWidth == 0 {
			// Segment alone exceeds maxWidth - need to break it
			if seg.Width > maxWidth {
				// Break the unbreakable
				lines = append(lines, breakHard(iter.text[iter.pos-seg.RuneLen:], maxWidth)...)
				current.Reset()
				currentWidth = 0
			} else {
				current.WriteString(seg.Text)
				currentWidth = seg.Width
			}
		} else {
			// Start new line
			lines = append(lines, current.String())
			current.Reset()
			current.WriteString(seg.Text)
			currentWidth = seg.Width
		}
	}

	if current.Len() > 0 {
		lines = append(lines, current.String())
	}

	return lines
}

// breakHard breaks a long string mid-word to fit maxWidth.
func breakHard(s string, maxWidth int) []string {
	var lines []string
	var current strings.Builder
	w := 0

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := RuneWidth(r)
		if r == '\n' {
			if current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
				w = 0
			}
			i += size
			continue
		}

		if w+rw > maxWidth && w > 0 {
			lines = append(lines, current.String())
			current.Reset()
			w = 0
		}

		current.WriteRune(r)
		w += rw
		i += size
	}

	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

// Indent applies an indent to each line, respecting maxWidth.
func Indent(text string, indent string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	indentWidth := StringWidth(indent)
	if indentWidth >= maxWidth {
		return strings.Repeat("\n", strings.Count(text, "\n")+1)
	}

	lines := Wrap(text, maxWidth-indentWidth)
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}
