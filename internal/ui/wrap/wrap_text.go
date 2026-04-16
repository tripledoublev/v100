package wrap

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SoftWrap wraps text at word boundaries to fit maxWidth columns.
// Unlike simple Wrap, it optimizes for readability by:
// - Preferring breaks at spaces/punctuation
// - Balancing line length
// - Handling hard breaks (newlines) specially
func SoftWrap(text string, maxWidth int, hangingIndent int) []Line {
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	if text == "" {
		return nil
	}

	maxWidth -= hangingIndent
	if maxWidth < 20 {
		maxWidth = 20 // minimum viable width
	}

	var lines []Line
	var line strings.Builder
	lineWidth := 0
	paraStart := true

	i := 0
	for i < len(text) {
		// Process one grapheme cluster at a time
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size

		// Handle newline (hard break)
		if r == '\n' {
			if line.Len() > 0 {
				lines = append(lines, Line{Text: line.String(), Width: lineWidth})
				line.Reset()
				lineWidth = 0
			}
			paraStart = true
			continue
		}

		// Get display width
		w := RuneWidth(r)

		// Handle tab
		if r == '\t' {
			w = TabWidth
		}

		// Would adding this rune exceed width?
		if lineWidth+w > maxWidth {
			// Try to break at word boundary
			if !paraStart && line.Len() > 0 {
				// Check if current line has a break point we can use
				content := line.String()
				breakIdx := lastWordBreak(content)

				if breakIdx > 0 {
					// Break at word boundary
					lines = append(lines, Line{
						Text:  strings.TrimRight(content[:breakIdx], " \t"),
						Width: StringWidth(content[:breakIdx]),
					})
					// Leftover becomes start of next line
					remainder := content[breakIdx:]
					line.Reset()
					line.WriteString(remainder)
					lineWidth = StringWidth(remainder)
				} else {
					// No word break, flush current line
					lines = append(lines, Line{Text: line.String(), Width: lineWidth})
					line.Reset()
					lineWidth = 0
				}

				paraStart = false

				// Retry adding current rune
				if w <= maxWidth {
					line.WriteRune(r)
					lineWidth += w
				}
				continue
			}

			// Line is empty or at paragraph start - flush and try again
			if line.Len() > 0 {
				lines = append(lines, Line{Text: line.String(), Width: lineWidth})
				line.Reset()
				lineWidth = 0
			}
			paraStart = false

			// If single character is too wide, truncate
			if w > maxWidth {
				// Take what fits
				continue
			}

			line.WriteRune(r)
			lineWidth += w
			paraStart = false
		} else {
			line.WriteRune(r)
			lineWidth += w
			if !unicode.IsSpace(r) && r != '\t' {
				paraStart = false
			}
		}
	}

	// Flush remaining
	if line.Len() > 0 {
		lines = append(lines, Line{Text: line.String(), Width: lineWidth})
	}

	return lines
}

// Line represents a wrapped line with its display width.
type Line struct {
	Text  string
	Width int
}

// lastWordBreak finds the last word boundary in s.
// Returns index of first char of last word, or 0 if none.
func lastWordBreak(s string) int {
	found := 0
	for i := len(s) - 1; i >= 0; i-- {
		r, _ := utf8.DecodeLastRuneInString(s[:i+1])
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if found > 0 {
				return i + 1
			}
		} else {
			found = i + 1
		}
	}
	return 0
}

// Fill wraps text to produce lines of approximately equal width.
// Useful for formatted output, tables, etc.
func Fill(text string, maxWidth int, targetLines int) []string {
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	if text == "" {
		return nil
	}

	// Get all words
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var lines []string

	// Simple greedy fill: accumulate words until width exceeded
	var line strings.Builder
	lineWidth := 0

	for _, word := range words {
		wordWidth := StringWidth(word)

		// Word alone too wide?
		if wordWidth > maxWidth {
			// Flush current line
			if line.Len() > 0 {
				lines = append(lines, line.String())
				line.Reset()
				lineWidth = 0
			}
			// Break long word
			lines = append(lines, breakWord(word, maxWidth)...)
			continue
		}

		// Would word fit?
		space := 0
		if line.Len() > 0 {
			space = 1
		}

		if lineWidth+space+wordWidth <= maxWidth {
			if line.Len() > 0 {
				line.WriteByte(' ')
				lineWidth++
			}
			line.WriteString(word)
			lineWidth += wordWidth
		} else {
			// Flush and start new line
			if line.Len() > 0 {
				lines = append(lines, line.String())
			}
			line.Reset()
			line.WriteString(word)
			lineWidth = wordWidth
		}
	}

	if line.Len() > 0 {
		lines = append(lines, line.String())
	}

	return lines
}

// breakWord breaks a long word to fit maxWidth.
func breakWord(word string, maxWidth int) []string {
	var lines []string
	var current strings.Builder
	w := 0

	for i := 0; i < len(word); {
		r, size := utf8.DecodeRuneInString(word[i:])
		rw := RuneWidth(r)

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

// Truncate returns text truncated to maxWidth, appending ellipsis if cut.
func Truncate(text string, maxWidth int) string {
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	if StringWidth(text) <= maxWidth {
		return text
	}

	var result strings.Builder
	w := 0
	ellipsis := "…"
	ellipsisWidth := StringWidth(ellipsis)
	maxContent := maxWidth - ellipsisWidth

	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		rw := RuneWidth(r)

		if w+rw > maxContent {
			break
		}

		result.WriteRune(r)
		w += rw
		i += size
	}

	result.WriteString(ellipsis)
	return result.String()
}