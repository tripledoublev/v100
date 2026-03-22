package main

import "testing"

func TestMatchesFileExactPath(t *testing.T) {
	if !matchesFile("src/config.json", "src/config.json", "config.json") {
		t.Fatal("expected exact path match")
	}
}

func TestMatchesFileRelativeTargetMatchesAbsoluteWritePath(t *testing.T) {
	if !matchesFile("/tmp/work/src/config.json", "src/config.json", "config.json") {
		t.Fatal("expected suffix path match")
	}
}

func TestMatchesFileBaseNameOnlyMatchesBaseName(t *testing.T) {
	if !matchesFile("/tmp/work/src/config.json", "config.json", "config.json") {
		t.Fatal("expected basename match")
	}
}

func TestMatchesFileDoesNotMatchDifferentDirectoriesWithSameBaseName(t *testing.T) {
	if matchesFile("tests/config.json", "src/config.json", "config.json") {
		t.Fatal("did not expect sibling path with same basename to match")
	}
}

func TestMatchesFileDoesNotUseSubstringMatch(t *testing.T) {
	if matchesFile("foo/src/config.json.bak", "src/config.json", "config.json") {
		t.Fatal("did not expect substring path match")
	}
}
