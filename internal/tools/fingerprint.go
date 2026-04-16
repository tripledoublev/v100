package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// fingerprintTool identifies songs via audio fingerprinting (chromaprint/AcoustID).
// It records a short audio sample from a URL, generates a fingerprint with fpcalc,
// and looks up the track via the AcoustID API.
type fingerprintTool struct{}

func Fingerprint() Tool { return &fingerprintTool{} }

func (t *fingerprintTool) Name() string { return "fingerprint" }
func (t *fingerprintTool) Description() string {
	return "Identify a song from an audio stream or file using chromaprint fingerprinting. " +
		"Records a short sample, generates an acoustic fingerprint via fpcalc, " +
		"and queries AcoustID to return artist, title, and MusicBrainz recording ID. " +
		"Requires fpcalc (chromaprint tools) and ffmpeg to be installed."
}
func (t *fingerprintTool) DangerLevel() DangerLevel { return Safe }
func (t *fingerprintTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, MutatesWorkspace: true}
}

func (t *fingerprintTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["audio_url"],
		"properties": {
			"audio_url":   {"type": "string", "description": "URL of the audio stream or path to a local audio file to fingerprint."},
			"duration":    {"type": "integer", "description": "Seconds of audio to record from stream for fingerprinting (default: 15, min: 10, max: 30). Ignored for local files.", "default": 15},
			"artist_hint": {"type": "string", "description": "Optional artist name to disambiguate when multiple matches are found."},
			"title_hint":  {"type": "string", "description": "Optional track title to disambiguate when multiple matches are found."}
		}
	}`)
}

func (t *fingerprintTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"artist":          {"type": "string"},
			"title":           {"type": "string"},
			"musicbrainz_id":   {"type": "string"},
			"acoustid_id":     {"type": "string"},
			"score":           {"type": "number"},
			"duration_seconds":{"type": "integer"},
			"fingerprint_ms":  {"type": "integer"},
			"lookup_ms":       {"type": "integer"},
			"error":           {"type": "string"}
		}
	}`)
}

func (t *fingerprintTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a struct {
		AudioURL   string `json:"audio_url"`
		Duration   int    `json:"duration"`
		ArtistHint string `json:"artist_hint"`
		TitleHint  string `json:"title_hint"`
	}
	a.Duration = 15
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	a.AudioURL = strings.TrimSpace(a.AudioURL)
	if a.AudioURL == "" {
		return failResult(start, "audio_url is required"), nil
	}
	if a.Duration < 10 {
		a.Duration = 10
	}
	if a.Duration > 30 {
		a.Duration = 30
	}

	// Check dependencies
	if _, err := exec.LookPath("fpcalc"); err != nil {
		return failResult(start, "fpcalc (chromaprint) not installed: "+err.Error()), nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return failResult(start, "ffmpeg not installed: "+err.Error()), nil
	}

	// Determine working directory
	workDir := call.WorkspaceDir
	if workDir == "" {
		workDir = "/tmp"
	}

	samplePath := filepath.Join(workDir, fmt.Sprintf("fp_sample_%d.wav", time.Now().UnixNano()))
	defer os.Remove(samplePath)

	// Record from URL or copy local file to wav
	if strings.HasPrefix(a.AudioURL, "http://") || strings.HasPrefix(a.AudioURL, "https://") {
		if err := t.recordStream(a.AudioURL, samplePath, a.Duration); err != nil {
			return failResult(start, "record failed: "+err.Error()), nil
		}
	} else {
		// Local file: convert to wav
		if err := t.convertToWav(a.AudioURL, samplePath); err != nil {
			return failResult(start, "convert to wav failed: "+err.Error()), nil
		}
	}

	// Generate fingerprint
	fingerprint, actualDuration, err := t.fingerprint(samplePath)
	if err != nil {
		return failResult(start, "fingerprint failed: "+err.Error()), nil
	}

	// Look up via AcoustID
	artist, title, mbID, aID, score, lookupMs, err := t.acoustIDLookup(fingerprint, actualDuration, a.ArtistHint, a.TitleHint)
	if err != nil {
		return failResult(start, "acoustid lookup failed: "+err.Error()), nil
	}

	result := map[string]any{
		"artist":            artist,
		"title":            title,
		"musicbrainz_id":   mbID,
		"acoustid_id":      aID,
		"score":            score,
		"duration_seconds": actualDuration,
		"fingerprint_ms":    time.Since(start).Milliseconds() - int64(lookupMs),
		"lookup_ms":        lookupMs,
	}

	// Check if we got a meaningful result
	if artist == "" && title == "" {
		result["error"] = "no match found"
	}

	out, _ := json.Marshal(result)
	return ToolResult{
		OK:         artist != "" && title != "",
		Output:     string(out),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// recordStream captures audio from a URL for `duration` seconds.
func (t *fingerprintTool) recordStream(streamURL, outputPath string, duration int) error {
	// Use ffmpeg to capture raw PCM then convert to 16-bit mono 11kHz WAV (chromaprint format)
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", streamURL,
		"-t", fmt.Sprintf("%d", duration),
		// chromaprint prefers: 16-bit mono 11kHz
		"-acodec", "pcm_s16le",
		"-ar", "11025",
		"-ac", "1",
		"-f", "wav",
		outputPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// convertToWav converts a local audio file to chromaprint-friendly format.
func (t *fingerprintTool) convertToWav(inputPath, outputPath string) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", inputPath,
		"-acodec", "pcm_s16le",
		"-ar", "11025",
		"-ac", "1",
		"-f", "wav",
		outputPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// fingerprint runs fpcalc on a WAV file and returns the fingerprint string
// and the actual duration in seconds.
func (t *fingerprintTool) fingerprint(wavPath string) (string, int, error) {
	cmd := exec.Command("fpcalc", "-raw", "-length", "0", wavPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("fpcalc error: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var fingerprint string
	var duration int
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "FINGERPRINT=") {
			fingerprint = strings.TrimPrefix(line, "FINGERPRINT=")
		}
		if strings.HasPrefix(line, "DURATION=") {
			fmt.Sscanf(strings.TrimPrefix(line, "DURATION="), "%d", &duration)
		}
	}

	if fingerprint == "" {
		return "", 0, fmt.Errorf("no fingerprint in fpcalc output")
	}
	return fingerprint, duration, nil
}

// acoustIDLookup submits a fingerprint to AcoustID and returns the best match.
func (t *fingerprintTool) acoustIDLookup(fingerprint string, duration int, artistHint, titleHint string) (
	artist, title, musicbrainzID, acoustID string, score float64, lookupMs int, err error,
) {
	lookupStart := time.Now()

	apiURL := "https://api.acoustid.org/v2/lookup?"
	data := url.Values{
		"client":     {"bIIiXkGN"},
		"meta":      {"recordings", "recordingids", "sources"},
		"duration":  {fmt.Sprintf("%d", duration)},
		"fingerprint": {fingerprint},
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", "", "", "", 0, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "v100-fingerprint/1.0 (github.com/tripledoublev/v100)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", "", 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", "", 0, 0, err
	}

	var result struct {
		Status   string `json:"status"`
		Results  []struct {
			ID     string `json:"id"`
			Score  float64 `json:"score"`
			Recordings []struct {
				ID       string `json:"id"`
				Duration int    `json:"duration"`
				Sources  []struct {
					ArtistName  string `json:"artist_name"`
					ReleaseName string `json:"release"`
					TrackName   string `json:"track_name"`
				} `json:"sources"`
			} `json:"recordings"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", "", "", 0, 0, fmt.Errorf("acoustid parse error: %v", err)
	}

	if result.Status != "ok" || len(result.Results) == 0 {
		return "", "", "", "", 0, 0, fmt.Errorf("no acoustid results")
	}

	lookupMs = int(time.Since(lookupStart).Milliseconds())

	// Find best match, optionally using hints
	var bestArtist, bestTitle, bestMBID, bestAID string
	var bestScore float64

	for _, r := range result.Results {
		for _, rec := range r.Recordings {
			for _, src := range rec.Sources {
				trackName := src.TrackName
				if trackName == "" {
					trackName = src.ReleaseName
				}
				if trackName == "" {
					continue
				}
				artistName := src.ArtistName
				if artistName == "" {
					artistName = "unknown"
				}

				// Score boost if hints match
				s := r.Score
				if artistHint != "" && strings.Contains(strings.ToLower(artistName), strings.ToLower(artistHint)) {
					s += 0.1
				}
				if titleHint != "" && strings.Contains(strings.ToLower(trackName), strings.ToLower(titleHint)) {
					s += 0.1
				}

				if s > bestScore {
					bestScore = s
					bestArtist = artistName
					bestTitle = trackName
					bestMBID = rec.ID
					bestAID = r.ID
				}
			}
		}
	}

	return bestArtist, bestTitle, bestMBID, bestAID, bestScore, lookupMs, nil
}
