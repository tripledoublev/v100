package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

func renderStructuredForPane(content string, width int) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if highlighted, ok := highlightJSONForPane(content, width); ok {
		return highlighted
	}
	return wrapPlainContent(content, width)
}

func highlightJSONForPane(content string, width int) (string, bool) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(content), "", "  "); err != nil {
		return "", false
	}

	lines := strings.Split(strings.TrimRight(pretty.String(), "\n"), "\n")
	for i, line := range lines {
		lines[i] = wrapStyledLine(highlightJSONLine(line), width)
	}
	return strings.Join(lines, "\n"), true
}

func highlightJSONLine(line string) string {
	var out strings.Builder
	i := 0
	for i < len(line) {
		ch := line[i]
		switch {
		case ch == ' ' || ch == '\t':
			out.WriteByte(ch)
			i++
		case ch == '"':
			end := scanJSONString(line, i)
			token := line[i:end]
			j := end
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j < len(line) && line[j] == ':' {
				out.WriteString(styleJSONKey.Render(token))
			} else {
				out.WriteString(styleJSONString.Render(token))
			}
			i = end
		case isJSONNumberStart(line, i):
			end := scanJSONNumber(line, i)
			out.WriteString(styleJSONNumber.Render(line[i:end]))
			i = end
		case strings.HasPrefix(line[i:], "true"):
			out.WriteString(styleJSONBool.Render("true"))
			i += 4
		case strings.HasPrefix(line[i:], "false"):
			out.WriteString(styleJSONBool.Render("false"))
			i += 5
		case strings.HasPrefix(line[i:], "null"):
			out.WriteString(styleJSONNull.Render("null"))
			i += 4
		case strings.ContainsRune("{}[]:,", rune(ch)):
			out.WriteString(styleJSONPunct.Render(string(ch)))
			i++
		default:
			out.WriteByte(ch)
			i++
		}
	}
	return out.String()
}

func wrapStyledLine(line string, width int) string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	return wrap.String(line, width)
}

func wrapPlainContent(content string, width int) string {
	wrapped := wrap.String(content, width)
	return wrap.String(wrapped, width)
}

func scanJSONString(s string, start int) int {
	i := start + 1
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == '"' {
			return i + 1
		}
		i++
	}
	return len(s)
}

func isJSONNumberStart(s string, i int) bool {
	if s[i] == '-' {
		return i+1 < len(s) && unicode.IsDigit(rune(s[i+1]))
	}
	return unicode.IsDigit(rune(s[i]))
}

func scanJSONNumber(s string, start int) int {
	i := start
	for i < len(s) && strings.ContainsRune("-+0123456789.eE", rune(s[i])) {
		i++
	}
	return i
}
