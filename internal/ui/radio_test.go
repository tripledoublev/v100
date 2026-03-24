package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeSongMatchPart(t *testing.T) {
	got := normalizeSongMatchPart("  A/B_Test-Track [Live].mp3  ")
	want := "a b test track live mp3"
	if got != want {
		t.Fatalf("normalizeSongMatchPart() = %q, want %q", got, want)
	}
}

func TestFindExistingDownloadedSongMatchesNormalizedArtistAndTitle(t *testing.T) {
	dir := t.TempDir()
	name := "Boards_of_Canada - Dayvan Cowboy [abc123].mp3"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findExistingDownloadedSong(dir, "Boards of Canada", "Dayvan Cowboy")
	if !ok {
		t.Fatal("expected existing song match")
	}
	if got != name {
		t.Fatalf("findExistingDownloadedSong() = %q, want %q", got, name)
	}
}

func TestFindExistingDownloadedSongIgnoresWrongArtistAndNonMP3(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Other Artist - Dayvan Cowboy [abc123].mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Boards of Canada - Dayvan Cowboy.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := findExistingDownloadedSong(dir, "Boards of Canada", "Dayvan Cowboy"); ok {
		t.Fatalf("unexpected match %q", got)
	}
}

func TestFindExistingDownloadedSongAllowsEmptyArtistWhenTitleMatches(t *testing.T) {
	dir := t.TempDir()
	name := "Mabuta [xyz789].mp3"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findExistingDownloadedSong(dir, "", "Mabuta")
	if !ok {
		t.Fatal("expected title-only match")
	}
	if got != name {
		t.Fatalf("findExistingDownloadedSong() = %q, want %q", got, name)
	}
}
