package tts

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"hello":                              "hello",
		"look at `foo` here":                 "look at here",
		"```go\nx := 1\n```\ndone":           "done",
		"see [docs](https://example.com)":    "see docs",
		"visit https://example.com for info": "visit for info",
		"   ":                                "",
		"this is **bold** text":              "this is bold text",
		"and *italic* words":                 "and italic words",
		"# Heading one":                      "Heading one",
		"- first\n- second":                  "first, second",
		"1. step one\n2. step two":           "step one, step two",
		"para one.\n\npara two":              "para one. para two",
		"line one\nline two":                 "line one, line two",
		"done.\n\nnext":                      "done. next",
		"> quoted line":                      "quoted line",
		"mix of **bold**, *em*, and `code`":  "mix of bold, em, and",
		"unbalanced *asterisk here":          "unbalanced asterisk here",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
