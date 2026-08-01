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

	escapedSpoiler := ""
	if strings.Contains(source, `\|\|`) {
		escapedSpoiler = escapedSpoilerSentinel(source)
		if escapedSpoiler == "" {
			return Result{Text: source}
		}
		source = strings.ReplaceAll(source, `\|\|`, escapedSpoiler)
	}
	b := &builder{source: []byte(source)}
	doc := markdown.Parser().Parse(text.NewReader(b.source))
	b.renderChildren(doc)
	b.trimTrailingNewlines()
	result := Result{Text: b.out.String(), Styles: b.styles}
	result = applySpoilers(result)
	if escapedSpoiler != "" {
		result.Text = strings.ReplaceAll(result.Text, escapedSpoiler, "||")
	}
	normalizeStyles(&result)
	return result
}

// escapedSpoilerSentinel returns a token that occupies the same two UTF-16
// code units as "||" but cannot collide with the input. Supplementary private
// use code points are ideal for normal text; the two-rune fallback covers the
// pathological case where every such scalar is already present. An empty
// result asks the caller to preserve an impossibly adversarial input literally.
func escapedSpoilerSentinel(source string) string {
	seenRunes := make(map[rune]struct{})
	runes := []rune(source)
	for _, r := range runes {
		seenRunes[r] = struct{}{}
	}
	for r := rune(0xF0000); r <= 0xFFFFD; r++ {
		if _, exists := seenRunes[r]; !exists {
			return string(r)
		}
	}
	seenPairs := make(map[uint32]struct{})
	for i := 1; i < len(runes); i++ {
		if runes[i-1] >= 0xE000 && runes[i-1] <= 0xF8FF && runes[i] >= 0xE000 && runes[i] <= 0xF8FF {
			key := uint32(runes[i-1]-0xE000)<<16 | uint32(runes[i]-0xE000)
			seenPairs[key] = struct{}{}
		}
	}
	for first := rune(0xE000); first <= 0xF8FF; first++ {
		for second := rune(0xE000); second <= 0xF8FF; second++ {
			key := uint32(first-0xE000)<<16 | uint32(second-0xE000)
			if _, exists := seenPairs[key]; !exists {
				return string([]rune{first, second})
			}
		}
	}
	return ""
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
		b.append(string(n.Segment.Value(b.source)))
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
		b.styled(Monospace, func() {
			for i := 0; i < n.Lines().Len(); i++ {
				segment := n.Lines().At(i)
				b.append(string(segment.Value(b.source)))
			}
		})
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
	var candidates []int
	inFence := false
	var fence byte
	var width int
	inIndentedCode := false
	spoilerOpen := false
	htmlClose := ""
	lineStart := 0
	for lineStart < len(buffer) {
		rel := strings.IndexByte(buffer[lineStart:], '\n')
		if rel < 0 {
			break
		}
		lineEnd := lineStart + rel + 1
		line := strings.TrimSuffix(buffer[lineStart:lineEnd], "\n")
		trimmed := strings.TrimLeft(line, " \t")
		ch, n, fenceRest := fenceMarker(line)
		if htmlClose != "" {
			if strings.Contains(strings.ToLower(line), htmlClose) {
				htmlClose = ""
				if !spoilerOpen {
					candidates = append(candidates, lineEnd)
				}
			}
			lineStart = lineEnd
			continue
		}
		if inFence {
			if ch == fence && n >= width && strings.TrimSpace(fenceRest) == "" {
				inFence = false
				if !spoilerOpen {
					candidates = append(candidates, lineEnd)
				}
			}
		} else if n >= 3 && validFenceOpener(ch, fenceRest) {
			inFence, fence, width = true, ch, n
		} else if close := htmlBlockClose(line); close != "" {
			htmlClose = close
			if strings.Contains(strings.ToLower(line), close) {
				htmlClose = ""
				if !spoilerOpen {
					candidates = append(candidates, lineEnd)
				}
			}
		} else if inIndentedCode {
			if strings.TrimSpace(line) == "" || isIndentedCodeLine(line) {
				lineStart = lineEnd
				continue
			}
			inIndentedCode = false
			candidates = append(candidates, lineStart)
			scanSpoilers(line, &spoilerOpen)
			if !spoilerOpen && isStandaloneBlock(trimmed) {
				candidates = append(candidates, lineEnd)
			}
		} else if strings.TrimSpace(line) != "" && isIndentedCodeLine(line) {
			inIndentedCode = true
		} else {
			scanSpoilers(line, &spoilerOpen)
			if !spoilerOpen && (strings.TrimSpace(line) == "" || isStandaloneBlock(trimmed)) {
				candidates = append(candidates, lineEnd)
			}
		}
		lineStart = lineEnd
	}
	if inIndentedCode && lineStart < len(buffer) {
		partial := buffer[lineStart:]
		if strings.TrimSpace(partial) != "" && !isIndentedCodeLine(partial) {
			candidates = append(candidates, lineStart)
		}
	}
	if len(candidates) == 0 {
		return "", buffer
	}
	safe := candidates[len(candidates)-1]
	if referenceStart, unresolved := unresolvedReferenceStart(buffer[:safe]); unresolved {
		safe = 0
		for _, candidate := range candidates {
			if candidate > referenceStart {
				break
			}
			safe = candidate
		}
	}
	return buffer[:safe], buffer[safe:]
}

func scanSpoilers(line string, open *bool) {
	for i := 0; i+1 < len(line); {
		if line[i:i+2] == "||" {
			*open = !*open
			i += 2
			continue
		}
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
	}
}

func unresolvedReferenceStart(source string) (int, bool) {
	doc := markdown.Parser().Parse(text.NewReader([]byte(source)))
	var literalText strings.Builder
	start := -1
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		textNode, ok := n.(*ast.Text)
		if !ok || hasAncestor(n, func(parent ast.Node) bool {
			switch parent.(type) {
			case *ast.CodeSpan, *ast.Link, *ast.Image, *ast.RawHTML:
				return true
			default:
				return false
			}
		}) {
			return ast.WalkContinue, nil
		}
		value := textNode.Segment.Value([]byte(source))
		if bracket := strings.IndexByte(string(value), '['); start < 0 && bracket >= 0 {
			start = textNode.Segment.Start + bracket
		}
		literalText.Write(value)
		return ast.WalkContinue, nil
	})
	text := literalText.String()
	unresolved := strings.IndexByte(text, '[') >= 0 && strings.IndexByte(text, ']') >= 0
	return start, unresolved
}

func hasAncestor(n ast.Node, match func(ast.Node) bool) bool {
	for parent := n.Parent(); parent != nil; parent = parent.Parent() {
		if match(parent) {
			return true
		}
	}
	return false
}

func isIndentedCodeLine(line string) bool {
	columns := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return columns >= 4
		}
		if columns >= 4 {
			return true
		}
	}
	return false
}

func htmlBlockClose(line string) string {
	lower := strings.ToLower(strings.TrimLeft(line, " \t"))
	for _, tag := range []string{"script", "pre", "style", "textarea"} {
		prefix := "<" + tag
		if strings.HasPrefix(lower, prefix) && (len(lower) == len(prefix) || lower[len(prefix)] == '>' || lower[len(prefix)] == ' ' || lower[len(prefix)] == '\t') {
			return "</" + tag + ">"
		}
	}
	switch {
	case strings.HasPrefix(lower, "<!--"):
		return "-->"
	case strings.HasPrefix(lower, "<?"):
		return "?>"
	case strings.HasPrefix(lower, "<![cdata["):
		return "]]>"
	}
	return ""
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

func fenceMarker(line string) (byte, int, string) {
	indent := 0
	for indent < len(line) && line[indent] == ' ' && indent < 4 {
		indent++
	}
	if indent > 3 || indent == len(line) || line[indent] == '\t' || (line[indent] != '`' && line[indent] != '~') {
		return 0, 0, ""
	}
	i := indent
	for i < len(line) && line[i] == line[indent] {
		i++
	}
	return line[indent], i - indent, line[i:]
}

func validFenceOpener(ch byte, rest string) bool {
	return ch != '`' || !strings.Contains(rest, "`")
}

func hasUnclosedFence(source string) bool {
	inFence := false
	var fence byte
	var width int
	for _, line := range strings.Split(source, "\n") {
		ch, n, rest := fenceMarker(line)
		if !inFence && n >= 3 && validFenceOpener(ch, rest) {
			inFence, fence, width = true, ch, n
		} else if inFence && ch == fence && n >= width && strings.TrimSpace(rest) == "" {
			inFence = false
		}
	}
	return inFence
}

func applySpoilers(in Result) Result {
	type marker struct {
		byte  int
		units int
	}
	type pair struct{ open, close marker }
	var pairs []pair
	var open *marker
	monospace := make([]Style, 0, len(in.Styles))
	for _, style := range in.Styles {
		if style.Kind == Monospace {
			monospace = append(monospace, style)
		}
	}
	sort.Slice(monospace, func(i, j int) bool { return monospace[i].Start < monospace[j].Start })
	monoIndex := 0
	units := 0
	for i := 0; i+1 < len(in.Text); {
		for monoIndex < len(monospace) && monospace[monoIndex].Start+monospace[monoIndex].Length <= units {
			monoIndex++
		}
		coveredByCode := monoIndex < len(monospace) && units >= monospace[monoIndex].Start
		if in.Text[i:i+2] != "||" || coveredByCode {
			r, size := utf8.DecodeRuneInString(in.Text[i:])
			i += size
			units += utf16.RuneLen(r)
			continue
		}
		current := marker{byte: i, units: units}
		if open == nil {
			open = &current
		} else {
			pairs = append(pairs, pair{*open, current})
			open = nil
		}
		i += 2
		units += 2
	}
	if len(pairs) == 0 {
		return in
	}

	markerPositions := make([]int, 0, len(pairs)*2)
	for _, p := range pairs {
		markerPositions = append(markerPositions, p.open.units, p.close.units)
	}
	removedBefore := func(pos int) int {
		return sort.SearchInts(markerPositions, pos) * 2
	}
	var out strings.Builder
	last := 0
	for _, p := range pairs {
		out.WriteString(in.Text[last:p.open.byte])
		out.WriteString(in.Text[p.open.byte+2 : p.close.byte])
		last = p.close.byte + 2
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
		originalStart := p.open.units
		originalEnd := p.close.units
		start := originalStart - removedBefore(originalStart)
		end := originalEnd - removedBefore(originalEnd)
		if end > start {
			styles = append(styles, Style{Start: start, Length: end - start, Kind: Spoiler})
		}
	}
	return Result{Text: out.String(), Styles: styles}
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
