package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const radioIPCSocket = "/tmp/v100-radio.sock"

// RadioStation represents a selectable radio station
type RadioStation struct {
	Name string // Display name
	URL  string // Stream URL
}

// availableStations defines the radio stations users can choose from
var availableStations = []RadioStation{
	{Name: "Radiojar", URL: "https://n04.radiojar.com/78cxy6wkxtzuv"},
	{Name: "NTS Radio 1", URL: "https://stream-relay-geo.ntslive.net/stream"},
	{Name: "NTS Radio 2", URL: "https://stream-relay-geo.ntslive.net/stream2"},
}


func (m *TUIModel) jumpToStation(idx int) {
	if idx < 0 || idx >= len(availableStations) {
		return
	}
	wasPlaying := m.radioPlaying
	if wasPlaying {
		m.stopRadio()
	}
	m.radioURL = availableStations[idx].URL
	if wasPlaying {
		m.startRadio()
	}
}

func (m *TUIModel) cycleStation(direction int) {
	// Find current station index
	currentIdx := -1
	for i, s := range availableStations {
		if s.URL == m.radioURL {
			currentIdx = i
			break
		}
	}

	// If current URL not in list, try to find by checking if it starts with known URL
	if currentIdx == -1 {
		for i, s := range availableStations {
			if strings.HasPrefix(m.radioURL, strings.TrimSuffix(s.URL, "/")) {
				currentIdx = i
				break
			}
		}
	}

	// Cycle to next/previous station
	newIdx := currentIdx + direction
	if newIdx < 0 {
		newIdx = len(availableStations) - 1
	} else if newIdx >= len(availableStations) {
		newIdx = 0
	}

	// Stop current radio if playing and switch station
	wasPlaying := m.radioPlaying
	if wasPlaying {
		m.stopRadio()
	}

	m.radioURL = availableStations[newIdx].URL

	if wasPlaying {
		m.startRadio()
	}

	m.statusLine = "radio: switched to " + availableStations[newIdx].Name
	m.statusMode = "radio"
}

// getCurrentStationName returns the name of the current station
func (m *TUIModel) getCurrentStationName() string {
	for _, s := range availableStations {
		if s.URL == m.radioURL {
			return s.Name
		}
	}
	// Check by prefix match
	for _, s := range availableStations {
		if strings.HasPrefix(m.radioURL, strings.TrimSuffix(s.URL, "/")) {
			return s.Name
		}
	}
	// Try to extract station ID for display
	stationID := m.radioStationID()
	if stationID != "" {
		return "Radio: " + stationID
	}
	return "Custom"
}

func radioTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return radioTickMsg{} })
}

func (m *TUIModel) onRadioTick() tea.Cmd {
	if !m.radioPlaying {
		m.radioWave = ""
		return nil
	}
	m.radioStep++
	levels := []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"}
	var b strings.Builder
	for i := 0; i < 96; i++ {
		idx := (m.radioStep + i*2 + (i % 7)) % len(levels)
		b.WriteString(levels[idx])
	}
	m.radioWave = b.String()
	// Poll now-playing metadata conservatively to avoid rate limits.
	if m.radioLastPoll.IsZero() || time.Since(m.radioLastPoll) >= 30*time.Second {
		m.radioLastPoll = time.Now()
		return fetchNowPlayingCmd(m.radioStationID())
	}
	return nil
}

func (m *TUIModel) toggleRadio() {
	if m.radioPlaying {
		m.stopRadio()
		return
	}
	m.startRadio()
}

func (m *TUIModel) adjustRadioVolume(delta int) {
	m.radioVolume = clampInt(m.radioVolume+delta, 0, 100)
	if !m.radioPlaying || m.radioPlayer != "mpv" {
		return
	}

	// Send volume update via IPC socket for mpv
	go func(vol int) {
		// Try to dial multiple times in case mpv is still starting
		var conn net.Conn
		var err error
		for i := 0; i < 3; i++ {
			conn, err = net.Dial("unix", radioIPCSocket)
			if err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		cmd := fmt.Sprintf(`{"command": ["set_property", "volume", %d]}`, vol)
		_, _ = conn.Write([]byte(cmd + "\n"))
	}(m.radioVolume)
}

func (m *TUIModel) radioStateLine() string {
	state := "idle"
	if m.radioPlaying {
		state = "playing"
	}
	stationName := m.getCurrentStationName()
	return fmt.Sprintf("%s  %s  vol=%d%%  Ctrl+R play/stop  [/] volume  N/P station", state, stationName, m.radioVolume)
}

func (m *TUIModel) startRadio() {
	m.radioErr = ""
	if m.radioURL == "" {
		m.radioURL = availableStations[0].URL
	}
	if m.radioPlayer == "" {
		m.radioPlayer = detectRadioPlayer()
	}
	if m.radioPlayer == "" {
		m.radioErr = "no player found (install mpv or ffplay)"
		return
	}

	var args []string
	switch m.radioPlayer {
	case "mpv":
		args = []string{
			"--no-video",
			"--no-terminal",
			"--really-quiet",
			fmt.Sprintf("--volume=%d", m.radioVolume),
			fmt.Sprintf("--input-ipc-server=%s", radioIPCSocket),
			m.radioURL,
		}
	case "ffplay":
		args = []string{"-nodisp", "-loglevel", "quiet", "-volume", strconv.Itoa(m.radioVolume), m.radioURL}
	default:
		m.radioErr = "unsupported player: " + m.radioPlayer
		return
	}

	cmd := exec.Command(m.radioPlayer, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		m.radioErr = "radio start failed: " + err.Error()
		return
	}
	m.radioCmd = cmd
	m.radioPlaying = true
	m.radioLastPoll = time.Time{}
	go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
}

func (m *TUIModel) stopRadio() {
	if m.radioCmd != nil && m.radioCmd.Process != nil {
		_ = m.radioCmd.Process.Kill()
	}
	m.radioCmd = nil
	m.radioPlaying = false
	m.radioWave = ""
	_ = os.Remove(radioIPCSocket)
}

func detectRadioPlayer() string {
	if _, err := exec.LookPath("mpv"); err == nil {
		return "mpv"
	}
	if _, err := exec.LookPath("ffplay"); err == nil {
		return "ffplay"
	}
	return ""
}

func fetchNowPlayingCmd(stationID string) tea.Cmd {
	if strings.TrimSpace(stationID) == "" {
		return nil
	}
	return func() tea.Msg {
		artist, title, err := fetchNowPlaying(stationID)
		if err != nil {
			return radioNowPlayingMsg{Err: "now-playing unavailable"}
		}
		return radioNowPlayingMsg{Artist: artist, Title: title}
	}
}

func (m *TUIModel) startDownloadCmd() tea.Cmd {
	stationID := m.radioStationID()
	if stationID == "" {
		m.radioErr = "no radio station configured"
		return nil
	}
	m.statusMode = "downloading"
	m.statusLine = "fetching song info…"
	m.radioErr = ""

	return func() tea.Msg {
		artist, title, err := fetchNowPlaying(stationID)
		if err != nil {
			return downloadDoneMsg{err: "now-playing unavailable"}
		}
		song := strings.TrimSpace(artist + " - " + title)
		query := strings.TrimSpace(artist + " " + title + " audio")
		if query == "" {
			return downloadDoneMsg{err: "empty song metadata"}
		}
		if _, err := exec.LookPath("yt-dlp"); err != nil {
			return downloadDoneMsg{err: "yt-dlp not installed"}
		}
		dir := "/home/v/Music/favorites"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return downloadDoneMsg{err: "cannot create favorites folder"}
		}
		metaPath := filepath.Join(dir, "favorites.txt")
		if f, err := os.OpenFile(metaPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString(time.Now().Format("2006-01-02 15:04:05") + " | " + song + "\n")
			_ = f.Close()
		}
		outTmpl := filepath.Join(dir, "%(title)s [%(id)s].%(ext)s")
		cmd := exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "-o", outTmpl, "ytsearch1:"+query)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			return downloadDoneMsg{err: "yt-dlp error"}
		}
		return downloadDoneMsg{artist: artist, title: title}
	}
}

func (m *TUIModel) radioStationID() string {
	u := strings.TrimSpace(strings.TrimSuffix(m.radioURL, "/"))
	if u == "" {
		return ""
	}
	i := strings.LastIndex(u, "/")
	if i < 0 || i+1 >= len(u) {
		return ""
	}
	return u[i+1:]
}

func (m *TUIModel) renderWaveForWidth(width int) string {
	if width < 8 {
		return "♪"
	}
	target := width
	if target < 6 {
		target = 6
	}
	if len(m.radioWave) >= target {
		return m.radioWave[:target]
	}
	if len(m.radioWave) == 0 {
		return "♪"
	}
	repeats := (target + len(m.radioWave) - 1) / len(m.radioWave)
	wave := strings.Repeat(m.radioWave, repeats)
	return wave[:target]
}

func fetchNowPlaying(stationID string) (string, string, error) {
	if stationID == "" {
		return "", "", fmt.Errorf("missing station id")
	}
	url := "https://proxy.radiojar.com/api/stations/" + stationID + "/now_playing/?callback=x"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	s := strings.TrimSpace(string(b))
	start := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if start < 0 || end <= start {
		return "", "", fmt.Errorf("unexpected payload")
	}
	var payload struct {
		Artist string `json:"artist"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(s[start+1:end]), &payload); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(payload.Artist), strings.TrimSpace(payload.Title), nil
}
