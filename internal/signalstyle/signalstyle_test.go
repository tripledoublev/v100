package signalstyle

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRenderInlineStylesAndUTF16Offsets(t *testing.T) {
	got := Render("**bold _and italic_** ~~gone~~ ||hidden|| `code` 😀")
	if got.Text != "bold and italic gone hidden code 😀" {
		t.Fatalf("Text = %q", got.Text)
	}
	want := []Style{
		{Start: 0, Length: 15, Kind: Bold},
		{Start: 5, Length: 10, Kind: Italic},
		{Start: 16, Length: 4, Kind: Strikethrough},
		{Start: 21, Length: 6, Kind: Spoiler},
		{Start: 28, Length: 4, Kind: Monospace},
	}
	if !reflect.DeepEqual(got.Styles, want) {
		t.Fatalf("Styles = %#v, want %#v", got.Styles, want)
	}
}

func TestRenderReadableBlocksAndLinks(t *testing.T) {
	input := "# Heading\n\n- one\n- **two**\n\n> quoted\n\n[site](https://example.test)\n\n```go\nfmt.Println(1)\n```"
	got := Render(input)
	want := "Heading\n• one\n• two\n> quoted\nsite (https://example.test)\nfmt.Println(1)"
	if got.Text != want {
		t.Fatalf("Text:\n%q\nwant:\n%q", got.Text, want)
	}
	assertStyleCovers(t, got, Bold, "Heading")
	assertStyleCovers(t, got, Bold, "two")
	assertStyleCovers(t, got, Monospace, "fmt.Println(1)")
}

func TestRenderNestedStylesWithEmoji(t *testing.T) {
	got := Render("**bold ||👩🏽‍💻 _secret_||**")
	if got.Text != "bold 👩🏽‍💻 secret" {
		t.Fatalf("Text = %q", got.Text)
	}
	assertStyleCovers(t, got, Bold, got.Text)
	assertStyleCovers(t, got, Spoiler, "👩🏽‍💻 secret")
	assertStyleCovers(t, got, Italic, "secret")
}

func TestRenderLinkDoesNotRepeatIdenticalLabel(t *testing.T) {
	got := Render("[https://example.test](https://example.test)")
	if got.Text != "https://example.test" {
		t.Fatalf("Text = %q", got.Text)
	}
}

func TestRenderMultilineQuoteAndList(t *testing.T) {
	got := Render("> first\n> second\n\n- item\n  continued")
	if got.Text != "> first\n> second\n• item\n  continued" {
		t.Fatalf("Text = %q", got.Text)
	}
}

func TestRenderMalformedMarkupLiterally(t *testing.T) {
	for _, input := range []string{
		"before **unclosed",
		"before ||unclosed",
		"[broken](https://example.test",
		"```go\nunclosed **code**",
	} {
		got := Render(input)
		if got.Text != input {
			t.Errorf("Render(%q).Text = %q", input, got.Text)
		}
	}
}

func TestRenderEscapedSpoilerAndCodePipes(t *testing.T) {
	got := Render("\\|\\|visible\\|\\| and `||code||`")
	if got.Text != "||visible|| and ||code||" {
		t.Fatalf("Text = %q", got.Text)
	}
	for _, s := range got.Styles {
		if s.Kind == Spoiler {
			t.Fatalf("unexpected spoiler: %#v", s)
		}
	}
	assertStyleCovers(t, got, Monospace, "||code||")
}

func TestRenderPreservesPrivateUseRunes(t *testing.T) {
	input := "\ue000\ue001 \U000f0000 literal and \\|\\|escaped\\|\\|"
	got := Render(input)
	if want := "\ue000\ue001 \U000f0000 literal and ||escaped||"; got.Text != want {
		t.Fatalf("Text = %q, want %q", got.Text, want)
	}
	for _, style := range got.Styles {
		if style.Kind == Spoiler {
			t.Fatalf("unexpected spoiler: %#v", style)
		}
	}
}

func TestRenderRawHTMLIsLiteralText(t *testing.T) {
	input := "<script>alert('not HTML')</script>"
	got := Render(input)
	if got.Text != input {
		t.Fatalf("Text = %q, want literal %q", got.Text, input)
	}
	if len(got.Styles) != 0 {
		t.Fatalf("Styles = %#v, want none", got.Styles)
	}
}

func TestRenderManySpoilersCompletesInLinearishTime(t *testing.T) {
	input := strings.Repeat("||x|| ", 2_000)
	start := time.Now()
	got := Render(input)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("rendering spoiler-heavy input took %v", elapsed)
	}
	if len(got.Styles) != 2_000 {
		t.Fatalf("got %d spoiler styles, want 2000", len(got.Styles))
	}
}

func TestStyleString(t *testing.T) {
	if got := (Style{Start: 3, Length: 5, Kind: Spoiler}).String(); got != "3:5:SPOILER" {
		t.Fatalf("String = %q", got)
	}
}

func TestChunkGraphemesAndRebasesStyles(t *testing.T) {
	// The family emoji is one grapheme but eleven UTF-16 code units. Combining
	// acute is kept with its base even though the cluster crosses maxUTF16.
	text := "A👨‍👩‍👧‍👦e\u0301Z"
	whole := Result{Text: text, Styles: []Style{{Start: 0, Length: UTF16Len(text), Kind: Bold}}}
	got := Chunk(whole, 3)
	texts := make([]string, len(got))
	for i, chunk := range got {
		texts[i] = chunk.Text
		if len(chunk.Styles) != 1 || chunk.Styles[0].Start != 0 || chunk.Styles[0].Length != UTF16Len(chunk.Text) {
			t.Fatalf("chunk %d styles = %#v", i, chunk.Styles)
		}
	}
	if want := []string{"A", "👨‍👩‍👧‍👦", "e\u0301Z"}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("chunks = %#v, want %#v", texts, want)
	}
}

func TestChunkClipsOverlappingRanges(t *testing.T) {
	got := Chunk(Result{Text: "abcdef", Styles: []Style{{Start: 1, Length: 4, Kind: Italic}}}, 3)
	want := []Result{
		{Text: "abc", Styles: []Style{{Start: 1, Length: 2, Kind: Italic}}},
		{Text: "def", Styles: []Style{{Start: 0, Length: 2, Kind: Italic}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chunk = %#v, want %#v", got, want)
	}
}

func TestSplitStream(t *testing.T) {
	tests := []struct {
		name, input, flush, rest string
		final                    bool
	}{
		{"paragraph waits", "hello **wor", "", "hello **wor", false},
		{"blank closes paragraph", "hello **world**\n\nnext", "hello **world**\n\n", "next", false},
		{"heading is complete block", "# ready\npartial", "# ready\n", "partial", false},
		{"fence waits", "```go\nfmt.Println(1)\n", "", "```go\nfmt.Println(1)\n", false},
		{"fence closes", "```go\nx\n```\nafter", "```go\nx\n```\n", "after", false},
		{"blank inside fence waits", "```\nx\n\n", "", "```\nx\n\n", false},
		{"indented closer is fence content", "```\nx\n    ```\n", "", "```\nx\n    ```\n", false},
		{"multiline spoiler waits", "||one\n\ntwo", "", "||one\n\ntwo", false},
		{"multiline spoiler closes", "||one\n\ntwo||\n\nnext", "||one\n\ntwo||\n\n", "next", false},
		{"unresolved reference waits", "[x]\n\nnext", "", "[x]\n\nnext", false},
		{"flushes before unresolved reference", "done\n\n[x]\n\nnext", "done\n\n", "[x]\n\nnext", false},
		{"reference definition resolves", "[x]\n\n[x]: /target\n\nnext", "[x]\n\n[x]: /target\n\n", "next", false},
		{"blank inside indented code waits", "    one\n\n", "", "    one\n\n", false},
		{"indented code closes before text", "    one\n\nnext", "    one\n\n", "next", false},
		{"blank inside raw HTML waits", "<script>\n\n", "", "<script>\n\n", false},
		{"raw HTML close flushes", "<script>\n\n</script>\nnext", "<script>\n\n</script>\n", "next", false},
		{"similar HTML tag uses blank boundary", "<scripture>\n\nnext", "<scripture>\n\n", "next", false},
		{"final flushes malformed", "hello **wor", "hello **wor", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flush, rest := SplitStream(tt.input, tt.final)
			if flush != tt.flush || rest != tt.rest {
				t.Fatalf("got (%q, %q), want (%q, %q)", flush, rest, tt.flush, tt.rest)
			}
			if flush+rest != tt.input {
				t.Fatal("split did not preserve input")
			}
		})
	}
}

func TestRenderUsesCommonMarkFenceIndentation(t *testing.T) {
	unclosed := "```\nx\n    ```\n"
	if got := Render(unclosed); got.Text != unclosed || len(got.Styles) != 0 {
		t.Fatalf("indented pseudo-closer was accepted: %#v", got)
	}

	indentedCode := "    ```\ntext"
	got := Render(indentedCode)
	if got.Text == indentedCode {
		t.Fatalf("four-space-indented code was misclassified as an unclosed fence")
	}
	assertStyleCovers(t, got, Monospace, "```")
}

func TestUTF16Len(t *testing.T) {
	if got := UTF16Len("a😀e\u0301"); got != 5 {
		t.Fatalf("UTF16Len = %d", got)
	}
}

func FuzzRenderRanges(f *testing.F) {
	for _, seed := range []string{"**bold**", "👩🏽‍💻 ||secret||", "`code`", "[x](https://x.test)"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got := Render(input)
		if !utf8.ValidString(input) {
			if got.Text != input {
				t.Fatal("invalid input was not preserved")
			}
			return
		}
		limit := UTF16Len(got.Text)
		for _, style := range got.Styles {
			if style.Start < 0 || style.Length <= 0 || style.Start+style.Length > limit {
				t.Fatalf("invalid style %#v for UTF-16 length %d", style, limit)
			}
		}
		chunks := Chunk(got, 17)
		var joined strings.Builder
		for _, chunk := range chunks {
			joined.WriteString(chunk.Text)
		}
		if joined.String() != got.Text {
			t.Fatal("chunking changed text")
		}
	})
}

func assertStyleCovers(t *testing.T, result Result, kind Kind, substring string) {
	t.Helper()
	byteStart := strings.Index(result.Text, substring)
	if byteStart < 0 {
		t.Fatalf("%q missing from %q", substring, result.Text)
	}
	start := UTF16Len(result.Text[:byteStart])
	length := UTF16Len(substring)
	for _, style := range result.Styles {
		if style.Kind == kind && style.Start <= start && style.Start+style.Length >= start+length {
			return
		}
	}
	t.Fatalf("no %s style covers %q in %#v", kind, substring, result.Styles)
}
