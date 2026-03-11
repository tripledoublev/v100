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
	lipgloss "github.com/charmbracelet/lipgloss"
)

const radioIPCSocket = "/tmp/v100-radio.sock"

// RadioStation represents a selectable radio station
type RadioStation struct {
	Name string // Display name
	URL  string // Stream URL
	Type string // "radiojar" or "nts"
	ID   string // Station ID for the API
}

// availableStations defines the radio stations users can choose from
var availableStations = []RadioStation{
	{
		Name: "Radio Al Hara",
		URL:  "https://n04.radiojar.com/78cxy6wkxtzuv",
		Type: "radiojar",
		ID:   "78cxy6wkxtzuv",
	},
	{
		Name: "NTS Radio 1",
		URL:  "https://stream-relay-geo.ntslive.net/stream",
		Type: "nts",
		ID:   "1",
	},
	{
		Name: "NTS Radio 2",
		URL:  "https://stream-relay-geo.ntslive.net/stream2",
		Type: "nts",
		ID:   "2",
	},
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

// getCurrentStation returns the current RadioStation object
func (m *TUIModel) getCurrentStation() *RadioStation {
	for _, s := range availableStations {
		if s.URL == m.radioURL {
			return &s
		}
	}
	// Check by prefix match
	for _, s := range availableStations {
		if strings.HasPrefix(m.radioURL, strings.TrimSuffix(s.URL, "/")) {
			return &s
		}
	}
	return nil
}

// getCurrentStationName returns the name of the current station
func (m *TUIModel) getCurrentStationName() string {
	s := m.getCurrentStation()
	if s != nil {
		return s.Name
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
		s := m.getCurrentStation()
		if s != nil {
			return fetchNowPlayingCmd(s.Type, s.ID)
		}
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

func fetchNowPlayingCmd(stationType, stationID string) tea.Cmd {
	if stationType == "" || stationID == "" {
		return nil
	}
	return func() tea.Msg {
		artist, title, err := fetchNowPlaying(stationType, stationID)
		if err != nil {
			return radioNowPlayingMsg{Err: "now-playing unavailable"}
		}
		return radioNowPlayingMsg{Artist: artist, Title: title}
	}
}

func (m *TUIModel) startDownloadCmd() tea.Cmd {
	s := m.getCurrentStation()
	if s == nil {
		m.radioErr = "no radio station configured"
		return nil
	}
	
	// Capture current metadata from the model to ensure consistency with UI
	artist := m.radioArtist
	title := m.radioTitle
	
	m.statusMode = "downloading"
	m.statusLine = "initializing download..."
	m.radioErr = ""

	return func() tea.Msg {
		// If current metadata is empty, try one fresh fetch
		if artist == "" && title == "" {
			var err error
			artist, title, err = fetchNowPlaying(s.Type, s.ID)
			if err != nil {
				return downloadDoneMsg{err: "now-playing unavailable"}
			}
		}
		
		song := strings.TrimSpace(artist + " - " + title)
		query := strings.TrimSpace(artist + " " + title + " audio")
		if query == "" || query == "NTS audio" {
			return downloadDoneMsg{err: "empty or invalid song metadata"}
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
			return downloadDoneMsg{err: "yt-dlp error: " + err.Error()}
		}
		return downloadDoneMsg{artist: artist, title: title}
	}
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

func fetchNowPlaying(stationType, stationID string) (string, string, error) {
	if stationID == "" {
		return "", "", fmt.Errorf("missing station id")
	}

	switch stationType {
	case "radiojar":
		return fetchRadiojarNowPlaying(stationID)
	case "nts":
		return fetchNTSNowPlaying(stationID)
	default:
		return "", "", fmt.Errorf("unsupported station type: %s", stationType)
	}
}

func fetchRadiojarNowPlaying(stationID string) (string, string, error) {
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

func fetchNTSNowPlaying(channel string) (string, string, error) {
	url := "https://www.nts.live/api/v2/live"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var liveData struct {
		Results []struct {
			ChannelName string `json:"channel_name"`
			Now         struct {
				BroadcastTitle string    `json:"broadcast_title"`
				StartTimestamp time.Time `json:"start_timestamp"`
				Embeds         struct {
					Details struct {
						Links []struct {
							Rel  string `json:"rel"`
							Href string `json:"href"`
						} `json:"links"`
					} `json:"details"`
				} `json:"embeds"`
			} `json:"now"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&liveData); err != nil {
		return "", "", err
	}

	var channelData *struct {
		ChannelName string `json:"channel_name"`
		Now         struct {
			BroadcastTitle string    `json:"broadcast_title"`
			StartTimestamp time.Time `json:"start_timestamp"`
			Embeds         struct {
				Details struct {
					Links []struct {
						Rel  string `json:"rel"`
						Href string `json:"href"`
					} `json:"links"`
				} `json:"details"`
			} `json:"embeds"`
		} `json:"now"`
	}

	for i := range liveData.Results {
		if liveData.Results[i].ChannelName == channel {
			channelData = &liveData.Results[i]
			break
		}
	}

	if channelData == nil {
		return "", "", fmt.Errorf("channel %s not found", channel)
	}

	showTitle := channelData.Now.BroadcastTitle
	tracklistURL := ""
	for _, l := range channelData.Now.Embeds.Details.Links {
		if l.Rel == "tracklist" {
			tracklistURL = l.Href
			break
		}
	}

	if tracklistURL == "" {
		return "NTS", showTitle, nil
	}

	// Fetch tracklist to find current song
	tResp, err := client.Get(tracklistURL)
	if err != nil {
		return "NTS", showTitle, nil
	}
	defer func() { _ = tResp.Body.Close() }()

	var tData struct {
		Results []struct {
			Artist string `json:"artist"`
			Title  string `json:"title"`
			Offset int    `json:"offset"`
		} `json:"results"`
	}
	if err := json.NewDecoder(tResp.Body).Decode(&tData); err != nil {
		return "NTS", showTitle, nil
	}

	elapsed := int(time.Since(channelData.Now.StartTimestamp).Seconds())
	
	// Find the track with the largest offset <= elapsed
	bestIdx := -1
	for i, t := range tData.Results {
		if t.Offset <= elapsed {
			if bestIdx == -1 || t.Offset > tData.Results[bestIdx].Offset {
				bestIdx = i
			}
		}
	}

	if bestIdx != -1 {
		return tData.Results[bestIdx].Artist, tData.Results[bestIdx].Title, nil
	}

	return "NTS", showTitle, nil
}

func (m *TUIModel) radioSelectView() string {
	var sb strings.Builder
	sb.WriteString(tuiHeaderStyle.Render("Radio Station Selector") + "\n\n")
	
	for i, s := range availableStations {
		prefix := "  "
		if i == m.radioSelectIdx {
			prefix = "> "
		}
		
		nameStr := s.Name
		if m.radioURL == s.URL {
			nameStr += " (playing)"
		}
		
		line := prefix + nameStr
		if i == m.radioSelectIdx {
			sb.WriteString(tuiInputActiveStyle.Render(line) + "\n")
		} else if m.radioURL == s.URL {
			sb.WriteString(tuiHeaderStyle.Render(line) + "\n")
		} else {
			sb.WriteString(tuiHeaderDimStyle.Render(line) + "\n")
		}
	}
	
	sb.WriteString("\n" + tuiHeaderDimStyle.Render("  ↑/↓: navigate   Enter: select   Esc: cancel"))
	
	box := tuiPaneStyle.Render(sb.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
