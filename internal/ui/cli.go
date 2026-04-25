package ui

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/tts"
	"golang.org/x/term"
)

// CLIRenderer prints events to stdout line by line with colors.
type CLIRenderer struct {
	agentStarts    map[string]time.Time // agentRunID → start time
	inSubAgent     int                  // nesting depth of sub-agents
	spinnerStop    chan struct{}
	spinnerDone    chan struct{}
	modelCallStart time.Time
	modelCallStop  chan struct{}
	modelCallDone  chan struct{}
	Verbose        bool
	streamedText   bool // set when EventModelToken prints; skip text in EventModelResp if true
	speaker        *tts.Speaker
	mu             sync.Mutex
	imageRenderer  *ImageRenderer
}

// EnableTTS attaches a background speaker that voices assistant text.
func (r *CLIRenderer) EnableTTS() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.speaker == nil {
		r.speaker = tts.NewSpeaker()
	}
}

// WaitForSpeech blocks until the TTS queue is fully drained. Safe to call
// when TTS is disabled — it returns immediately.
func (r *CLIRenderer) WaitForSpeech() {
	r.mu.Lock()
	sp := r.speaker
	r.mu.Unlock()
	if sp != nil {
		sp.Wait()
	}
}

// Close releases renderer resources (e.g. the TTS speaker).
func (r *CLIRenderer) Close() {
	r.mu.Lock()
	sp := r.speaker
	r.speaker = nil
	r.mu.Unlock()
	if sp != nil {
		sp.Close()
	}
}

type PromptResult struct {
	Text   string
	Images [][]byte
}

// NewCLIRenderer creates a CLI renderer.
func NewCLIRenderer() *CLIRenderer {
	return &CLIRenderer{
		agentStarts:   make(map[string]time.Time),
		imageRenderer: NewImageRenderer(),
	}
}

// formatTokens formats token count as "Nk" for ≥1000 tokens, or "N" for <1000.
// Fix #6: Show actual token count when <1k instead of rounding to "0k"
func formatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// stopModelSpinner stops the model-call spinner and waits for it to finish.
// It returns true when a spinner was active, so callers can add a clean line
// break before rendering assistant output.
func (r *CLIRenderer) stopModelSpinner() bool {
	r.mu.Lock()
	active := r.modelCallStop != nil
	if r.modelCallStop != nil {
		close(r.modelCallStop)
		r.modelCallStop = nil
	}
	done := r.modelCallDone
	r.modelCallDone = nil
	r.mu.Unlock()
	if done != nil {
		<-done
	}
	return active
}

// RenderEvent prints a human-readable, colorized representation of an event.
func (r *CLIRenderer) RenderEvent(ev core.Event) {
	ts := styleMuted.Render(ev.TS.Local().Format(time.TimeOnly))

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Println(RunBanner(ev.RunID, p.Provider, p.Model))

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Source == "system" {
			fmt.Printf("\n%s  %s  %s\n",
				ts,
				styleWarn.Render("system"),
				p.Content,
			)
			break
		}
		content := p.Content
		if p.ImageCount > 0 {
			if content != "" {
				content += " "
			}
			content += imageCount(p.ImageCount)
		}
		indented := indentLines(content, "              ")
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			styleUser.Render(userMessageLabel),
			indented,
		)

	case core.EventModelCall:
		if r.inSubAgent > 0 {
			break
		}
		// Reset streaming state for every model call, regardless of TTY.
		r.mu.Lock()
		r.streamedText = false
		r.mu.Unlock()
		// Skip spinner when stdout is not a TTY (pipe/file redirect)
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			break
		}
		r.mu.Lock()
		r.modelCallStart = time.Now()
		r.modelCallStop = make(chan struct{})
		r.modelCallDone = make(chan struct{})
		stop := r.modelCallStop
		done := r.modelCallDone
		start := r.modelCallStart
		r.mu.Unlock()
		go func() {
			defer close(done)
			frames := []string{"-", "\\", "|", "/"}
			i := 0
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					fmt.Print("\r\033[K")
					return
				case <-ticker.C:
					elapsed := time.Since(start).Round(time.Second)
					fmt.Printf("\r  %s  %s thinking... %s",
						styleMuted.Render("⎿"),
						styleMuted.Render(frames[i%len(frames)]),
						styleMuted.Render(elapsed.String()),
					)
					i++
				}
			}
		}()

	case core.EventModelResp:
		if r.stopModelSpinner() {
			fmt.Println()
		}
		if r.inSubAgent > 0 {
			break // suppress sub-agent model responses
		}
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Consume streaming state under mutex to avoid data race with EventModelToken.
		r.mu.Lock()
		streamedText := r.streamedText
		r.streamedText = false
		r.mu.Unlock()
		// Skip printing text if it was already streamed via EventModelToken.
		if p.Text != "" && !streamedText {
			indented := indentLines(p.Text, "              ")
			fmt.Printf("%s  %s  %s\n",
				ts,
				styleAssistant.Render("agent"),
				indented,
			)
		}
		if p.Text != "" && r.speaker != nil {
			r.speaker.Speak(p.Text)
		}
		// If text was streamed and there are tool calls, print a blank line for separation.
		if streamedText && len(p.ToolCalls) > 0 {
			fmt.Print("")
		}
		for _, tc := range p.ToolCalls {
			args := TruncateOutput(tc.ArgsJSON, r.Verbose)
			argsTokens := estimateTokens(tc.ArgsJSON)
			fmt.Printf("           %s %s%s  %s\n",
				styleTool.Render("tool"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+args+")"),
				styleMuted.Render(fmt.Sprintf("[~%d tokens]", argsTokens)),
			)
		}

	case core.EventModelToken:
		if r.inSubAgent > 0 {
			break
		}
		if r.stopModelSpinner() {
			fmt.Println()
		}
		var p map[string]string
		_ = json.Unmarshal(ev.Payload, &p)
		r.mu.Lock()
		r.streamedText = true
		r.mu.Unlock()
		fmt.Print(p["text"])

	case core.EventToolCallDelta:
		if r.inSubAgent > 0 {
			break
		}
		var p core.ToolCallDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		// Tool call deltas are hard to render cleanly in line-by-line CLI.
		// We could print a dot or just ignore and wait for the final tool call result.
		// For research fidelity, we'll ignore in CLI but log in trace.

	case core.EventToolOutputDelta:
		if r.inSubAgent > 0 {
			break
		}
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		delta := strings.ReplaceAll(p.Delta, "\n", " ↵ ")
		if len(delta) > 200 {
			delta = delta[:200] + "…"
		}
		fmt.Printf("           %s %s  %s\n",
			styleMuted.Render("↳"),
			styleMuted.Render(p.Stream),
			delta,
		)

	case core.EventToolCall:
		// Suppress sub-agent tool calls; top-level shown inline in EventModelResp.
		break

	case core.EventToolResult:
		if r.stopModelSpinner() {
			fmt.Println()
		}
		if r.inSubAgent > 0 {
			break // suppress sub-agent tool results
		}
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		var statusLabel, statusStyle string
		if p.OK {
			statusLabel = styleOK.Render("ok")
			statusStyle = styleOK.Render(p.Name)
		} else {
			statusLabel = styleFail.Render("err")
			statusStyle = styleFail.Render(p.Name)
		}
		// Use SmartSummary for intelligent output display, then estimate tokens.
		out := SmartSummary(p.Name, p.Output, r.Verbose)
		outTokens := estimateTokens(p.Output)
		fmt.Printf("           %s %s  %s  %s  %s\n",
			statusLabel,
			statusStyle,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			styleMuted.Render(fmt.Sprintf("[~%d tokens]", outTokens)),
			out,
		)

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Fprintf(os.Stderr, "\n%s  %s  %s\n",
			ts,
			styleFail.Render("error"),
			styleFail.Render(p.Error),
		)

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n", EndBanner(p.Reason, ev.RunID, p.UsedSteps, p.UsedTokens))
		if p.Summary != "" {
			fmt.Printf("%s\n", styleInfo.Render("Summary: "+p.Summary))
		}

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		r.mu.Lock()
		r.agentStarts[p.AgentRunID] = ev.TS
		r.inSubAgent++
		r.mu.Unlock()
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "Agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "Dispatch:" + p.Agent
		}
		fmt.Printf("\n%s\n", styleInfo.Render(fmt.Sprintf("● %s(%s)", label, task)))
		// Start spinner
		r.mu.Lock()
		r.spinnerStop = make(chan struct{})
		r.spinnerDone = make(chan struct{})
		r.mu.Unlock()
		go r.runSpinner()

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		pat := ""
		if p.Pattern != "" {
			pat = " [" + p.Pattern + "]"
		}
		fmt.Printf("\n%s\n", styleInfo.Render(fmt.Sprintf("● Orchestrate%s %s", pat, task)))

	case core.EventAgentEnd:
		// Stop spinner
		r.mu.Lock()
		if r.spinnerStop != nil {
			close(r.spinnerStop)
			r.spinnerStop = nil
		}
		done := r.spinnerDone
		r.mu.Unlock()
		if done != nil {
			<-done
		}
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		r.mu.Lock()
		startTime := r.agentStarts[p.AgentRunID]
		delete(r.agentStarts, p.AgentRunID)
		r.inSubAgent--
		if r.inSubAgent < 0 {
			r.inSubAgent = 0
		}
		r.mu.Unlock()
		dur := ev.TS.Sub(startTime)
		summary := fmt.Sprintf("%d tool uses · %s · %s",
			p.ToolUses, FormatTokens(p.UsedTokens), FormatDuration(dur.Milliseconds()))
		if p.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", p.CostUSD)
		}
		if p.OK {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleOK.Render("Done")+" "+styleMuted.Render("("+summary+")"))
		} else {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleFail.Render("Failed")+" "+styleMuted.Render("("+summary+")"))
		}

	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("%s  %s  %s\n",
			ts,
			styleInfo.Render("⊘ "+compressEventLabel(p.Trigger)),
			styleMuted.Render(fmt.Sprintf("%d→%d msgs  ~%dk→%dk tok  $%.4f",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000, p.CostUSD)),
		)

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s  %s  %s\n",
			ts,
			stylePrimary.Render(fmt.Sprintf("── step %d ──", p.StepNumber)),
			styleMuted.Render(fmt.Sprintf("tok=%s  $%.4f  %d tools  %d model calls  %dms",
				formatTokens(p.InputTokens), p.CostUSD, p.ToolCalls, p.ModelCalls, p.DurationMS)),
		)

	case core.EventImageInline:
		if r.inSubAgent > 0 {
			break
		}
		var p core.ImageInlinePayload
		_ = json.Unmarshal(ev.Payload, &p)
		data, _ := base64.StdEncoding.DecodeString(p.Data)
		img := ""
		if r.imageRenderer != nil {
			img = r.imageRenderer.Render(data, 80, 0)
		} else {
			img = RenderImageInlineAuto(data, 80)
		}
		if img != "" {
			fmt.Printf("\n%s\n", img)
		}

	default:
		fmt.Printf("%s  %s\n", ts, styleMuted.Render(string(ev.Type)))
	}
}

// runSpinner displays a cycling spinner until spinnerStop is closed.
func (r *CLIRenderer) runSpinner() {
	r.mu.Lock()
	stop := r.spinnerStop
	done := r.spinnerDone
	r.mu.Unlock()
	defer close(done)

	frames := []string{"-", "\\", "|", "/"}
	i := 0
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Print("\r\033[K") // clear spinner line
			return
		case <-ticker.C:
			fmt.Printf("\r  %s  %s working...", styleMuted.Render("⎿"), styleMuted.Render(frames[i%len(frames)]))
			i++
		}
	}
}

// ConfirmTool prompts the user on stdin to approve a dangerous tool call.
func ConfirmTool(toolName, args string) bool {
	// Fix #2: Detect if stdin is not a TTY (e.g., piped input) and auto-deny
	// to prevent commands like /quit from being misinterpreted as confirmation
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "⚠  Dangerous tool %q denied: stdin is not a terminal (piped/scripted input)\n", toolName)
		return false
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(clrDanger).
		Padding(0, 2).
		Render(
			styleDanger.Render("⚠  DANGEROUS TOOL: "+toolName) + "\n" +
				styleMuted.Render("Args: ") + args + "\n\n" +
				styleWarn.Render("Approve?") + "  " +
				styleOK.Render("[y]") + " yes   " +
				styleFail.Render("[N]") + " no",
		)
	fmt.Printf("\n%s\n\n", box)

	// Use raw-mode reads (same as promptTerminal) instead of bufio.Scanner.
	// bufio.Scanner expects cooked/line-buffered input, which deadlocks when
	// the terminal is in raw mode — keyboard input appears frozen and Ctrl+C
	// doesn't work because raw mode maps it to byte 0x03, not SIGINT.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠  Could not set terminal to raw mode: %v\n", err)
		return false
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	fmt.Print(styleWarn.Render("▸ "))

	var buf [1]byte
	for {
		if _, err := os.Stdin.Read(buf[:]); err != nil {
			return false
		}
		switch b := buf[0]; b {
		case 'y', 'Y':
			_, _ = fmt.Fprintf(os.Stdout, "\r\n")
			return true
		case 'n', 'N', '\r', '\n':
			_, _ = fmt.Fprintf(os.Stdout, "\r\n")
			return false
		case 0x03: // Ctrl+C in raw mode
			_, _ = fmt.Fprintf(os.Stdout, "^C\r\n")
			return false
		case 0x1b: // escape sequence (arrow keys etc.) — drain and ignore
			var seq [2]byte
			_, _ = os.Stdin.Read(seq[:])
		default:
			// ignore other keypresses
		}
	}
}

// Prompt prints a styled prompt and reads a line from stdin.
// Suppresses the prompt character in non-interactive (piped) mode.
func Prompt(prompt string) (string, error) {
	res, err := PromptWithImages(prompt)
	return res.Text, err
}

func PromptWithImages(prompt string) (PromptResult, error) {
	_ = prompt
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return PromptResult{}, err
			}
			return PromptResult{}, fmt.Errorf("EOF")
		}
		return PromptResult{Text: scanner.Text()}, nil
	}
	return promptTerminal(os.Stdin, os.Stdout, clipboardImageReader)
}

func promptTerminal(in *os.File, out io.Writer, readClipboard func() ([]byte, error)) (PromptResult, error) {
	oldState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return PromptResult{}, err
	}
	defer func() { _ = term.Restore(int(in.Fd()), oldState) }()

	var (
		text      []byte
		images    [][]byte
		statusMsg string
	)
	// lastLines tracks how many visual lines the previous render occupied
	// so we can clean them all up on the next render.
	var lastLines int

	render := func() {
		// Calculate how many visual lines the current content occupies.
		// We use a conservative 80-column width since we don't have the
		// actual terminal width in raw mode.
		promptLen := 2 // "▸ "
		extraLen := 0
		if len(images) > 0 {
			extraLen += len(imageCount(len(images))) + 1
		}
		if statusMsg != "" {
			extraLen += len(statusMsg) + 1
		}
		contentLen := promptLen + len(string(text)) + extraLen
		linesOccupied := (contentLen + 79) / 80
		if linesOccupied < 1 {
			linesOccupied = 1
		}

		// Clear all lines that were occupied by the previous render.
		for i := 0; i < lastLines; i++ {
			_, _ = fmt.Fprint(out, "\r\033[K")
			if i < lastLines-1 {
				_, _ = fmt.Fprint(out, "\033[B") // move down one line
			}
		}
		// Move cursor back to the beginning of the first line.
		if lastLines > 1 {
			_, _ = fmt.Fprintf(out, "\033[%dA", lastLines-1)
		}

		// Redraw the prompt and current content.
		_, _ = fmt.Fprint(out, stylePrimary.Render("▸")+" ")
		_, _ = fmt.Fprint(out, string(text))
		if len(images) > 0 {
			_, _ = fmt.Fprint(out, " "+styleInfo.Render(imageCount(len(images))))
		}
		if statusMsg != "" {
			_, _ = fmt.Fprint(out, " "+styleMuted.Render(statusMsg))
		}

		lastLines = linesOccupied
	}
	render()

	var buf [1]byte
	for {
		if _, err := in.Read(buf[:]); err != nil {
			if err == io.EOF {
				clearPromptLine(out)
				_, _ = fmt.Fprintln(out)
				return PromptResult{}, fmt.Errorf("EOF")
			}
			return PromptResult{}, err
		}
		statusMsg = ""
		switch b := buf[0]; b {
		case '\r', '\n':
			clearPromptLine(out)
			return PromptResult{
				Text:   string(text),
				Images: append([][]byte(nil), images...),
			}, nil
		case 0x03:
			clearPromptLine(out)
			_, _ = fmt.Fprintln(out)
			return PromptResult{}, fmt.Errorf("interrupt")
		case 0x7f, 0x08:
			if len(text) > 0 {
				_, size := utf8.DecodeLastRune(text)
				if size <= 0 {
					size = 1
				}
				text = text[:len(text)-size]
			}
		case 0x16:
			img, err := readClipboard()
			if err != nil {
				statusMsg = "paste failed: " + err.Error()
			} else {
				images = append(images, img)
				statusMsg = "attached " + imageCount(len(images))
			}
		case 0x1b:
			var seq [2]byte
			_, _ = in.Read(seq[:])
		default:
			text = append(text, b)
			for !utf8.Valid(text) {
				if _, err := in.Read(buf[:]); err != nil {
					return PromptResult{}, err
				}
				text = append(text, buf[0])
			}
		}
		render()
	}
}

func clearPromptLine(out io.Writer) {
	_, _ = fmt.Fprint(out, "\r\033[K")
}

func promptLine(text string, images [][]byte, statusMsg string) string {
	var buf bytes.Buffer
	buf.WriteString("▸ ")
	buf.WriteString(text)
	if len(images) > 0 {
		buf.WriteByte(' ')
		buf.WriteString(imageCount(len(images)))
	}
	if statusMsg != "" {
		buf.WriteByte(' ')
		buf.WriteString(statusMsg)
	}
	return buf.String()
}

// PrintReplayEvent prints a styled replay view of a trace event.
func PrintReplayEvent(ev core.Event) {
	ts := ev.TS.Local().Format("15:04:05")

	switch ev.Type {
	case core.EventRunStart:
		var p core.RunStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n\n", RunBanner(ev.RunID, p.Provider, p.Model))

	case core.EventUserMsg:
		var p core.UserMsgPayload
		_ = json.Unmarshal(ev.Payload, &p)
		borderClr := clrUser
		label := styleUser.Render(userMessageLabel)
		if p.Source == "system" {
			borderClr = clrWarning
			label = styleWarn.Render("v100")
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderClr).
			Padding(0, 1).
			Render(
				label + styleMuted.Render("  "+ts) + "\n" +
					p.Content,
			)
		fmt.Printf("\n%s\n", box)

	case core.EventModelResp:
		var p core.ModelRespPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if p.Text != "" {
			meta := styleMuted.Render(fmt.Sprintf("  %s  in=%d out=%d cost=$%.4f",
				ts, p.Usage.InputTokens, p.Usage.OutputTokens, p.Usage.CostUSD))
			box := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrAssistant).
				Padding(0, 1).
				Render(
					styleAssistant.Render("v100") + meta + "\n" +
						p.Text,
				)
			fmt.Printf("\n%s\n", box)
		}
		for _, tc := range p.ToolCalls {
			fmt.Printf("  %s %s%s\n",
				styleTool.Render("⚙"),
				styleTool.Render(tc.Name),
				styleMuted.Render("("+tc.ArgsJSON+")"),
			)
		}

	case core.EventToolCall:
		// Covered inline in EventModelResp above.

	case core.EventToolResult:
		var p core.ToolResultPayload
		_ = json.Unmarshal(ev.Payload, &p)
		icon := styleOK.Render("✓")
		nameStyle := styleOK.Render(p.Name)
		if !p.OK {
			icon = styleFail.Render("✗")
			nameStyle = styleFail.Render(p.Name)
		}
		out := p.Output
		if len(out) > 500 {
			out = out[:500] + "\n  … (truncated)"
		}
		fmt.Printf("  %s %s %s\n    %s\n",
			icon, nameStyle,
			styleMuted.Render(fmt.Sprintf("[%dms]", p.DurationMS)),
			strings.ReplaceAll(out, "\n", "\n    "),
		)

	case core.EventToolOutputDelta:
		var p core.ToolOutputDeltaPayload
		_ = json.Unmarshal(ev.Payload, &p)
		delta := p.Delta
		if len(delta) > 500 {
			delta = delta[:500] + "\n  … (truncated)"
		}
		fmt.Printf("  %s %s\n    %s\n",
			styleMuted.Render("↳ "+p.Stream),
			styleMuted.Render(p.Name),
			strings.ReplaceAll(delta, "\n", "\n    "),
		)

	case core.EventRunError:
		var p core.RunErrorPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s %s\n", styleFail.Render("✗ error:"), styleFail.Render(p.Error))

	case core.EventRunEnd:
		var p core.RunEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n%s\n\n", EndBanner(p.Reason, ev.RunID, p.UsedSteps, p.UsedTokens))

	case core.EventAgentStart:
		var p core.AgentStartPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		label := "Agent"
		if strings.TrimSpace(p.Agent) != "" {
			label = "Dispatch:" + p.Agent
		}
		fmt.Printf("\n  %s\n", styleInfo.Render(fmt.Sprintf("● %s(%s)", label, task)))

	case core.EventAgentDispatch:
		var p core.AgentDispatchPayload
		_ = json.Unmarshal(ev.Payload, &p)
		task := p.Task
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		pat := ""
		if p.Pattern != "" {
			pat = " [" + p.Pattern + "]"
		}
		fmt.Printf("  %s\n", styleInfo.Render(fmt.Sprintf("● Orchestrate%s %s", pat, task)))

	case core.EventAgentEnd:
		var p core.AgentEndPayload
		_ = json.Unmarshal(ev.Payload, &p)
		summary := fmt.Sprintf("%d tool uses · %s", p.ToolUses, FormatTokens(p.UsedTokens))
		if p.CostUSD > 0 {
			summary += fmt.Sprintf(" · $%.4f", p.CostUSD)
		}
		if p.OK {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleOK.Render("Done")+" "+styleMuted.Render("("+summary+")"))
		} else {
			fmt.Printf("  %s  %s\n", styleMuted.Render("⎿"), styleFail.Render("Failed")+" "+styleMuted.Render("("+summary+")"))
		}

	case core.EventCompress:
		var p core.CompressPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s  %s\n",
			styleInfo.Render("⊘ "+compressEventLabel(p.Trigger)),
			styleMuted.Render(fmt.Sprintf("%d→%d msgs  ~%dk→%dk tok  $%.4f",
				p.MessagesBefore, p.MessagesAfter,
				p.TokensBefore/1000, p.TokensAfter/1000, p.CostUSD)),
		)

	case core.EventStepSummary:
		var p core.StepSummaryPayload
		_ = json.Unmarshal(ev.Payload, &p)
		fmt.Printf("\n  %s  %s\n",
			stylePrimary.Render(fmt.Sprintf("── step %d ──", p.StepNumber)),
			styleMuted.Render(fmt.Sprintf("tok=%dk  $%.4f  %d tools  %d model calls  %dms",
				p.InputTokens/1000, p.CostUSD, p.ToolCalls, p.ModelCalls, p.DurationMS)),
		)

	case core.EventImageInline:
		var p core.ImageInlinePayload
		_ = json.Unmarshal(ev.Payload, &p)
		data, _ := base64.StdEncoding.DecodeString(p.Data)
		img := renderImageSummary(data)
		if img != "" {
			fmt.Printf("\n%s\n", img)
		}

	default:
		fmt.Printf("%s  %s\n", styleMuted.Render(ts), styleMuted.Render(string(ev.Type)))
	}
}

// indentLines adds a prefix to every line after the first.
func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
