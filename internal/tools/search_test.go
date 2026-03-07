package tools

import "testing"

func TestParseGitignoreExcludes(t *testing.T) {
	in := `
# comments ignored
runs/
.gocache/
node_modules
dist/*.map
!keep.me
`
	got := parseGitignoreExcludes(in)
	contains := func(want string) bool {
		for _, v := range got {
			if v == want {
				return true
			}
		}
		return false
	}
	wants := []string{
		"runs/**",
		"**/runs/**",
		".gocache/**",
		"**/.gocache/**",
		"node_modules",
		"**/node_modules",
		"**/node_modules/**",
		"dist/*.map",
	}
	for _, w := range wants {
		if !contains(w) {
			t.Fatalf("expected parsed excludes to contain %q; got=%v", w, got)
		}
	}
	if contains("keep.me") {
		t.Fatalf("negated pattern should not be included")
	}
}
