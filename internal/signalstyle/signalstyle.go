// Package signalstyle turns a conservative subset of Markdown into plain text
// plus Signal text-style ranges. All offsets and lengths are UTF-16 code units,
// as required by signal-cli.
package signalstyle

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Kind is a signal-cli text style name.
type Kind string

const (
	Bold          Kind = "BOLD"
	Italic        Kind = "ITALIC"
	Strikethrough Kind = "STRIKETHROUGH"
	Spoiler       Kind = "SPOILER"
	Monospace     Kind = "MONOSPACE"
)

func (k Kind) String() string { return string(k) }

// Style describes a styled UTF-16 range in Result.Text.
type Style struct {
	Start  int
	Length int
	Kind   Kind
}

// String returns signal-cli's start:length:STYLE representation.
func (s Style) String() string {
	return fmt.Sprintf("%d:%d:%s", s.Start, s.Length, s.Kind)
}

// Result is rendered text and its Signal style ranges.
type Result struct {
	Text   string
	Styles []Style
}

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough),
	goldmark.WithParserOptions(parser.WithEscapedSpace()),
)

// UTF16Len returns the number of UTF-16 code units in s.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// Render converts Markdown to readable plain text and Signal style ranges.
// Invalid UTF-8 and unclosed fenced code are returned literally.
func Render(source string) Result {
	if !utf8.ValidString(source) || hasUnclosedFence(source) {
		return Result{Text: source}
	}

	const escapedSpoiler = "\ue000\ue001"
	source = strings.ReplaceAll(source, `\|\|`, escapedSpoiler)
	b := &builder{source: []byte(source)}
	doc := markdown.Parser().Parse(text.NewReader(b.source))
	b.renderChildren(doc)
	b.trimTrailingNewlines()
	result := Result{Text: b.out.String(), Styles: b.styles}
	result = applySpoilers(result)
	result.Text = strings.ReplaceAll(result.Text, escapedSpoiler, "||")
	normalizeStyles(&result)
	return result
}

type builder struct {
	source []byte
	out    strings.Builder
	styles []Style
	list   []listState
	quote  int
}

type listState struct {
	ordered bool
	next    int
}

func (b *builder) pos() int { return UTF16Len(b.out.String()) }

func (b *builder) append(s string) { b.out.WriteString(s) }

func (b *builder) styled(kind Kind, fn func()) {
	start := b.pos()
	fn()
	if length := b.pos() - start; length > 0 {
		b.styles = append(b.styles, Style{Start: start, Length: length, Kind: kind})
	}
}

func (b *builder) renderChildren(parent ast.Node) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		b.render(n)
	}
}

func (b *builder) render(n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		b.append(string(n.Text(b.source)))
		if n.HardLineBreak() || n.SoftLineBreak() {
			b.append("\n")
			if n.NextSibling() != nil {
				b.append(b.continuationPrefix())
			}
		}
	case *ast.String:
		b.append(string(n.Value))
	case *ast.Emphasis:
		kind := Italic
		if n.Level == 2 {
			kind = Bold
		}
		b.styled(kind, func() { b.renderChildren(n) })
	case *ext.Strikethrough:
		b.styled(Strikethrough, func() { b.renderChildren(n) })
	case *ast.CodeSpan:
		b.styled(Monospace, func() { b.renderChildren(n) })
	case *ast.FencedCodeBlock:
		b.ensureBlockPrefix()
		b.styled(Monospace, func() { b.append(string(n.Text(b.source))) })
		b.ensureNewline()
	case *ast.CodeBlock:
		b.ensureBlockPrefix()
		b.styled(Monospace, func() {
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				b.append(string(segment.Value(b.source)))
			}
		})
		b.ensureNewline()
	case *ast.Heading:
		b.ensureBlockPrefix()
		b.styled(Bold, func() { b.renderChildren(n) })
		b.ensureNewline()
	case *ast.Paragraph:
		if _, inListItem := n.Parent().(*ast.ListItem); !inListItem {
			b.ensureBlockPrefix()
		}
		b.renderChildren(n)
		b.ensureNewline()
	case *ast.TextBlock:
		b.renderChildren(n)
		b.ensureNewline()
	case *ast.Blockquote:
		b.quote++
		b.renderChildren(n)
		b.quote--
	case *ast.List:
		b.list = append(b.list, listState{ordered: n.IsOrdered(), next: n.Start})
		b.renderChildren(n)
		b.list = b.list[:len(b.list)-1]
	case *ast.ListItem:
		b.ensureNewline()
		if b.quote > 0 {
			b.append(strings.Repeat("> ", b.quote))
		}
		indent := strings.Repeat("  ", max(0, len(b.list)-1))
		marker := "• "
		if len(b.list) > 0 && b.list[len(b.list)-1].ordered {
			s := &b.list[len(b.list)-1]
			marker = fmt.Sprintf("%d. ", s.next)
			s.next++
		}
		b.append(indent + marker)
		b.renderChildren(n)
	case *ast.Link:
		start := b.out.Len()
		b.renderChildren(n)
		label := b.out.String()[start:]
		dest := string(n.Destination)
		if dest != "" && label != dest {
			b.append(" (" + dest + ")")
		}
	case *ast.AutoLink:
		b.append(string(n.URL(b.source)))
	case *ast.Image:
		b.append("[image: ")
		b.renderChildren(n)
		b.append("]")
	case *ast.ThematicBreak:
		b.ensureBlockPrefix()
		b.append("———")
		b.ensureNewline()
	case *ast.RawHTML:
		b.append(string(n.Segments.Value(b.source)))
	case *ast.HTMLBlock:
		for i := 0; i < n.Lines().Len(); i++ {
			segment := n.Lines().At(i)
			b.append(string(segment.Value(b.source)))
		}
	default:
		b.renderChildren(n)
	}
}

func (b *builder) ensureNewline() {
	if b.out.Len() > 0 && !strings.HasSuffix(b.out.String(), "\n") {
		b.append("\n")
	}
}

func (b *builder) ensureBlockPrefix() {
	if b.out.Len() > 0 && !strings.HasSuffix(b.out.String(), "\n") {
		b.append("\n")
	}
	if b.quote > 0 {
		b.append(strings.Repeat("> ", b.quote))
	}
}

func (b *builder) continuationPrefix() string {
	return strings.Repeat("> ", b.quote) + strings.Repeat("  ", len(b.list))
}

func (b *builder) trimTrailingNewlines() {
	s := strings.TrimRight(b.out.String(), "\n")
	b.out.Reset()
	b.out.WriteString(s)
}

// Chunk splits a rendered result into chunks no longer than maxUTF16. It never
// splits a grapheme cluster; style ranges are clipped and rebased per chunk.
func Chunk(result Result, maxUTF16 int) []Result {
	if result.Text == "" {
		return nil
	}
	if maxUTF16 <= 0 {
		return []Result{result}
	}

	type boundary struct{ byte, units int }
	boundaries := []boundary{{}}
	g := uniseg.NewGraphemes(result.Text)
	units := 0
	for g.Next() {
		units += UTF16Len(g.Str())
		_, end := g.Positions()
		boundaries = append(boundaries, boundary{byte: end, units: units})
	}

	chunks := make([]Result, 0, units/maxUTF16+1)
	for start := 0; start < len(boundaries)-1; {
		end := start + 1
		for end < len(boundaries) && boundaries[end].units-boundaries[start].units <= maxUTF16 {
			end++
		}
		end--
		if end == start { // one cluster exceeds the limit; keep it intact
			end++
		}
		lo, hi := boundaries[start], boundaries[end]
		chunk := Result{Text: result.Text[lo.byte:hi.byte]}
		for _, style := range result.Styles {
			s := max(style.Start, lo.units)
			e := min(style.Start+style.Length, hi.units)
			if s < e {
				chunk.Styles = append(chunk.Styles, Style{Start: s - lo.units, Length: e - s, Kind: style.Kind})
			}
		}
		chunks = append(chunks, chunk)
		start = end
	}
	return chunks
}

// SplitStream returns complete Markdown blocks that are safe to render and the
// suffix that may still be extended by later stream data. final flushes all.
func SplitStream(buffer string, final bool) (flush, rest string) {
	if final {
		return buffer, ""
	}
	safe := 0
	inFence := false
	var fence byte
	var width int
	lineStart := 0
	for lineStart < len(buffer) {
		rel := strings.IndexByte(buffer[lineStart:], '\n')
		if rel < 0 {
			break
		}
		lineEnd := lineStart + rel + 1
		line := strings.TrimSuffix(buffer[lineStart:lineEnd], "\n")
		trimmed := strings.TrimLeft(line, " \t")
		ch, n := fenceMarker(trimmed)
		if inFence {
			if ch == fence && n >= width && strings.TrimSpace(trimmed[n:]) == "" {
				inFence = false
				safe = lineEnd
			}
		} else if n >= 3 {
			inFence, fence, width = true, ch, n
		} else if strings.TrimSpace(line) == "" || isStandaloneBlock(trimmed) {
			safe = lineEnd
		}
		lineStart = lineEnd
	}
	return buffer[:safe], buffer[safe:]
}

func isStandaloneBlock(line string) bool {
	if strings.HasPrefix(line, "#") {
		i := 0
		for i < len(line) && line[i] == '#' && i < 6 {
			i++
		}
		return i > 0 && i < len(line) && (line[i] == ' ' || line[i] == '\t')
	}
	trim := strings.TrimSpace(line)
	return trim == "---" || trim == "***" || trim == "___"
}

func fenceMarker(line string) (byte, int) {
	if line == "" || (line[0] != '`' && line[0] != '~') {
		return 0, 0
	}
	i := 0
	for i < len(line) && line[i] == line[0] {
		i++
	}
	return line[0], i
}

func hasUnclosedFence(source string) bool {
	inFence := false
	var fence byte
	var width int
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		ch, n := fenceMarker(trimmed)
		if !inFence && n >= 3 {
			inFence, fence, width = true, ch, n
		} else if inFence && ch == fence && n >= width && strings.TrimSpace(trimmed[n:]) == "" {
			inFence = false
		}
	}
	return inFence
}

func applySpoilers(in Result) Result {
	type pair struct{ open, close int }
	var pairs []pair
	open := -1
	for i := 0; i+1 < len(in.Text); {
		if in.Text[i:i+2] != "||" || covered(in.Styles, UTF16Len(in.Text[:i]), Monospace) {
			_, n := utf8.DecodeRuneInString(in.Text[i:])
			i += n
			continue
		}
		if open < 0 {
			open = i
		} else {
			pairs = append(pairs, pair{open, i})
			open = -1
		}
		i += 2
	}
	if len(pairs) == 0 {
		return in
	}

	removedBefore := func(pos int) int {
		n := 0
		for _, p := range pairs {
			if UTF16Len(in.Text[:p.open]) < pos {
				n += 2
			}
			if UTF16Len(in.Text[:p.close]) < pos {
				n += 2
			}
		}
		return n
	}
	var out strings.Builder
	last := 0
	for _, p := range pairs {
		out.WriteString(in.Text[last:p.open])
		out.WriteString(in.Text[p.open+2 : p.close])
		last = p.close + 2
	}
	out.WriteString(in.Text[last:])

	styles := make([]Style, 0, len(in.Styles)+len(pairs))
	for _, s := range in.Styles {
		start := s.Start - removedBefore(s.Start)
		end := s.Start + s.Length - removedBefore(s.Start+s.Length)
		if end > start {
			styles = append(styles, Style{Start: start, Length: end - start, Kind: s.Kind})
		}
	}
	for _, p := range pairs {
		originalStart := UTF16Len(in.Text[:p.open])
		originalEnd := UTF16Len(in.Text[:p.close])
		start := originalStart - removedBefore(originalStart)
		end := originalEnd - removedBefore(originalEnd)
		if end > start {
			styles = append(styles, Style{Start: start, Length: end - start, Kind: Spoiler})
		}
	}
	return Result{Text: out.String(), Styles: styles}
}

func covered(styles []Style, pos int, kind Kind) bool {
	for _, s := range styles {
		if s.Kind == kind && pos >= s.Start && pos < s.Start+s.Length {
			return true
		}
	}
	return false
}

func normalizeStyles(result *Result) {
	limit := UTF16Len(result.Text)
	clean := result.Styles[:0]
	for _, s := range result.Styles {
		start := max(0, s.Start)
		end := min(limit, s.Start+s.Length)
		if start < end {
			clean = append(clean, Style{Start: start, Length: end - start, Kind: s.Kind})
		}
	}
	sort.SliceStable(clean, func(i, j int) bool {
		if clean[i].Start != clean[j].Start {
			return clean[i].Start < clean[j].Start
		}
		if clean[i].Length != clean[j].Length {
			return clean[i].Length > clean[j].Length
		}
		return clean[i].Kind < clean[j].Kind
	})
	result.Styles = clean
}
