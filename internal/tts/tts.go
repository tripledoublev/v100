// Package tts provides a simple background text-to-speech speaker for v100.
//
// The speaker enqueues utterances and speaks them sequentially via an external
// command (default: espeak-ng). Override the command with V100_TTS_CMD, e.g.
//
//	V100_TTS_CMD="espeak-ng -s 180 -v en-us"
//	V100_TTS_CMD="piper --model en_US-amy-medium.onnx --output-raw | aplay -r 22050 -f S16_LE"
//
// If V100_TTS_CMD contains a shell pipe, the speaker runs it through `sh -c`.
package tts

import (
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// Speaker speaks text in the background, one utterance at a time.
type Speaker struct {
	queue  chan string
	wg     sync.WaitGroup
	active sync.WaitGroup
	once   sync.Once
	stop   chan struct{}
}

// NewSpeaker starts a speaker goroutine. Call Close to drain and stop it.
func NewSpeaker() *Speaker {
	s := &Speaker{
		queue: make(chan string, 32),
		stop:  make(chan struct{}),
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Speak enqueues text for speaking. Non-blocking; drops if queue is full.
func (s *Speaker) Speak(text string) {
	clean := sanitize(text)
	if clean == "" {
		return
	}
	s.active.Add(1)
	select {
	case s.queue <- clean:
	default:
		// queue full — drop rather than stall the main loop
		s.active.Done()
	}
}

// Wait blocks until all enqueued utterances have finished playing.
func (s *Speaker) Wait() {
	s.active.Wait()
}

// Close stops the speaker after draining in-flight utterances.
func (s *Speaker) Close() {
	s.once.Do(func() {
		close(s.queue)
	})
	s.wg.Wait()
}

func (s *Speaker) run() {
	defer s.wg.Done()
	for text := range s.queue {
		speakOne(text)
		s.active.Done()
	}
}

func speakOne(text string) {
	cmdStr := strings.TrimSpace(os.Getenv("V100_TTS_CMD"))
	var cmd *exec.Cmd
	switch {
	case cmdStr == "":
		cmd = exec.Command("espeak-ng", "--")
	case strings.ContainsAny(cmdStr, "|<>&;"):
		cmd = exec.Command("sh", "-c", cmdStr)
	default:
		parts := strings.Fields(cmdStr)
		cmd = exec.Command(parts[0], parts[1:]...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return
	}
	_, _ = io.WriteString(stdin, text)
	_ = stdin.Close()
	_ = cmd.Wait()
}

var (
	reCodeFence   = regexp.MustCompile("(?s)```.*?```")
	reInline      = regexp.MustCompile("`[^`]*`")
	reURL         = regexp.MustCompile(`https?://\S+`)
	reMdLink      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reImage       = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	reBoldItalic  = regexp.MustCompile(`\*\*\*([^*\n]+?)\*\*\*`)
	reBold        = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	reItalicStar  = regexp.MustCompile(`\*([^*\n]+?)\*`)
	reBoldU       = regexp.MustCompile(`__([^_\n]+?)__`)
	reItalicU     = regexp.MustCompile(`_([^_\n]+?)_`)
	reHeading     = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	reBlockquote  = regexp.MustCompile(`(?m)^\s*>\s?`)
	reListBullet  = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	reListNumber  = regexp.MustCompile(`(?m)^\s*\d+\.\s+`)
	reHR          = regexp.MustCompile(`(?m)^\s*(?:-{3,}|\*{3,}|_{3,})\s*$`)
	reStrayMdChar = regexp.MustCompile("[`*_~#]")
	reParaBreak   = regexp.MustCompile(`\n{2,}`)
	reHSpace      = regexp.MustCompile(`[ \t]+`)
)

// sanitize strips markdown noise unsuitable for TTS.
func sanitize(s string) string {
	s = reCodeFence.ReplaceAllString(s, " ")
	s = reImage.ReplaceAllString(s, " ")
	s = reMdLink.ReplaceAllString(s, "$1")
	s = reInline.ReplaceAllString(s, " ")
	s = reURL.ReplaceAllString(s, " ")
	s = reHR.ReplaceAllString(s, " ")
	s = reHeading.ReplaceAllString(s, "")
	s = reBlockquote.ReplaceAllString(s, "")
	s = reListBullet.ReplaceAllString(s, "")
	s = reListNumber.ReplaceAllString(s, "")
	s = reBoldItalic.ReplaceAllString(s, "$1")
	s = reBold.ReplaceAllString(s, "$1")
	s = reItalicStar.ReplaceAllString(s, "$1")
	s = reBoldU.ReplaceAllString(s, "$1")
	s = reItalicU.ReplaceAllString(s, "$1")
	// Drop any lingering markdown punctuation (unbalanced emphasis, table pipes, etc.).
	s = reStrayMdChar.ReplaceAllString(s, "")
	// Preserve pacing: paragraph breaks → sentence pause, single line breaks → short pause.
	s = reParaBreak.ReplaceAllString(s, "\x00PARA\x00")
	s = strings.ReplaceAll(s, "\n", "\x00LINE\x00")
	s = addPauseBeforeMarker(s, "\x00PARA\x00", ". ")
	s = addPauseBeforeMarker(s, "\x00LINE\x00", ", ")
	s = reHSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, ".,:;!? ")
	return strings.TrimSpace(s)
}

// addPauseBeforeMarker replaces marker with replacement, skipping the leading
// punctuation in replacement when the preceding char is already a sentence
// terminator (so we don't produce "?. " or ",, ").
func addPauseBeforeMarker(s, marker, replacement string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		prev := byte(' ')
		for j := i - 1; j >= 0; j-- {
			if s[j] != ' ' && s[j] != '\t' {
				prev = s[j]
				break
			}
		}
		switch prev {
		case '.', '!', '?', ',', ':', ';':
			b.WriteByte(' ')
		default:
			b.WriteString(replacement)
		}
		s = s[i+len(marker):]
	}
	return b.String()
}
