package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

// ConfirmFn is called before executing a dangerous tool.
// Returns true if the user approved the action.
type ConfirmFn func(toolName, args string) bool

// OutputFn is called for each event emitted during the loop.
type OutputFn func(event Event)

// Loop is the main agent execution engine.
type Loop struct {
	Run              *Run
	Provider         providers.Provider
	CompressProvider providers.Provider // cheap model for summarization; nil = use l.Provider
	Tools            *tools.Registry
	Policy           *policy.Policy
	Trace            *TraceWriter
	Budget           *BudgetTracker
	Messages         []providers.Message
	ConfirmFn        ConfirmFn
	OutputFn         OutputFn
	GenParams        providers.GenParams
	Solver           Solver
	Session          executor.Session
	Mapper           *PathMapper
	ModelMetadata    providers.ModelMetadata
	NetworkTier      string
	Hooks            []PolicyHook

	Snapshots SnapshotManager
	stepCount int // running step counter for step.summary events
	ended     bool
	mu        sync.Mutex

	lastToolOK     bool
	lastToolOutput string
	lastToolName   string
	lastToolArgs   string

	memoryStepID       string
	memoryStepMessage  string
	memoryStepEligible bool
	memoryStepConsumed bool
	pendingUserImages  []providers.ImageAttachment
}

const (
	budgetCompressionMinContextTokens = 500
	budgetCompressionLookaheadSteps   = 4
)

func (l *Loop) runHooks(stepID string, stage HookStage) HookResult {
	if len(l.Hooks) == 0 {
		return HookResult{Action: HookContinue}
	}

	state := LoopState{
		RunID:           l.Run.ID,
		Stage:           stage,
		StepCount:       l.stepCount,
		MessageCount:    len(l.Messages),
		LastToolOK:      l.lastToolOK,
		LastToolOutput:  l.lastToolOutput,
		LastToolName:    l.lastToolName,
		LastToolArgs:    l.lastToolArgs,
		BudgetRemaining: l.Budget.Budget(),
		// CompressionCount could be tracked if needed, using stats for now
	}

	for _, hook := range l.Hooks {
		res := hook(state)
		if res.Action != HookContinue {
			actionStr := ""
			switch res.Action {
			case HookInjectMessage:
				actionStr = "inject_message"
			case HookForceReplan:
				actionStr = "force_replan"
			case HookStopTools:
				actionStr = "stop_tools"
			case HookTerminate:
				actionStr = "terminate"
			}
			_, _ = l.emit(EventHookIntervention, stepID, HookInterventionPayload{
				Action:  actionStr,
				Message: res.Message,
				Reason:  res.Reason,
			})
			return res
		}
	}

	return HookResult{Action: HookContinue}
}

func (l *Loop) applyHookResult(hres HookResult, injectRole string, toolsStopped *bool) (bool, error) {
	switch hres.Action {
	case HookContinue:
		return false, nil
	case HookInjectMessage:
		if strings.TrimSpace(hres.Message) != "" {
			l.Messages = append(l.Messages, providers.Message{
				Role:    injectRole,
				Content: hres.Message,
			})
		}
		return true, nil
	case HookStopTools:
		msg := strings.TrimSpace(hres.Message)
		if msg == "" {
			msg = "System intervention: tool use is now disabled for the remainder of this step."
		}
		l.Messages = append(l.Messages, providers.Message{
			Role:    injectRole,
			Content: msg,
		})
		if toolsStopped != nil {
			*toolsStopped = true
		}
		return true, nil
	case HookForceReplan:
		msg := strings.TrimSpace(hres.Message)
		if msg == "" {
			msg = "System intervention: please discard your current plan and reassess."
		}
		l.Messages = append(l.Messages, providers.Message{
			Role:    injectRole,
			Content: msg,
		})
		return true, nil
	case HookTerminate:
		return false, fmt.Errorf("hook terminated: %s", hres.Reason)
	default:
		return false, nil
	}
}

// Step processes a single user input through the full model + tool execution cycle.
func (l *Loop) Step(ctx context.Context, userInput string) error {
	return l.StepWithImages(ctx, userInput, nil)
}

// StepWithImages processes a single user input and optional image attachments.
func (l *Loop) StepWithImages(ctx context.Context, userInput string, images []providers.ImageAttachment) error {
	if len(images) > 0 && !l.Provider.Capabilities().Images {
		return fmt.Errorf("provider %q does not support image attachments", l.Provider.Name())
	}
	// Auto-discover metadata on first step if not set
	if l.ModelMetadata.Model == "" {
		m, err := l.Provider.Metadata(ctx, "")
		if err == nil {
			l.ModelMetadata = m
		}
	}
	l.pendingUserImages = append([]providers.ImageAttachment(nil), images...)

	solver := l.Solver
	if solver == nil {
		solver = &ReactSolver{}
	}
	_, err := solver.Solve(ctx, l, userInput)
	l.pendingUserImages = nil
	return err
}

func (l *Loop) appendUserMessage(stepID, userInput string) error {
	payload := UserMsgPayload{
		Content:    userInput,
		ImageCount: len(l.pendingUserImages),
	}
	if _, err := l.emit(EventUserMsg, stepID, payload); err != nil {
		return err
	}

	// If the last message is also from the user (e.g. previous turn was interrupted),
	// merge the content to avoid consecutive user messages which many providers reject.
	if len(l.Messages) > 0 && l.Messages[len(l.Messages)-1].Role == "user" {
		last := &l.Messages[len(l.Messages)-1]
		if last.Content != "" && userInput != "" {
			last.Content += "\n\n" + userInput
		} else if userInput != "" {
			last.Content = userInput
		}
		last.Images = append(last.Images, l.pendingUserImages...)
		l.pendingUserImages = nil
		return nil
	}

	msg := providers.Message{
		Role:    "user",
		Content: userInput,
		Images:  append([]providers.ImageAttachment(nil), l.pendingUserImages...),
	}
	l.pendingUserImages = nil
	l.Messages = append(l.Messages, msg)
	return nil
}

// emitErrorAssistance tries one tool-free model turn to explain a failure and suggest remediation.
// If that fails, it emits a local fallback response so the transcript still guides the user.
// Skipped for context.Canceled errors since they're intentional user interruptions.
func (l *Loop) emitErrorAssistance(ctx context.Context, stepID string, cause error) {
	// Don't emit error assistance for user cancellations
	if errors.Is(cause, context.Canceled) {
		return
	}

	// Sanitize unresolved tool calls before building error assistance messages.
	_ = l.SanitizeLiveMessages()

	msgs := append([]providers.Message{}, l.buildMessages(false)...)
	msgs = append(msgs, providers.Message{
		Role: "user",
		Content: "System error encountered while processing your request:\n" + cause.Error() +
			"\n\nPlease explain what likely happened and propose concrete next steps/remediation.",
	})

	resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    l.Run.ID,
		StepID:   stepID,
		Messages: msgs,
		Tools:    nil, // explanatory turn only; avoid cascading tool errors
		Model:    "",
	})
	if err != nil {
		fallback := "I hit an internal error and couldn't run a recovery explanation step.\n" +
			"Error: " + cause.Error() + "\n" +
			"Next steps: verify credentials/network, retry the command, and inspect the last tool result in the transcript."
		_, _ = l.emit(EventModelResp, stepID, ModelRespPayload{
			Text: fallback,
			Usage: Usage{
				InputTokens:  0,
				OutputTokens: 0,
				CostUSD:      0,
			},
		})
		l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: fallback})
		return
	}

	text := resp.AssistantText
	if strings.TrimSpace(text) == "" {
		text = "I hit an error but didn't receive additional diagnostic text. Please inspect the run.error and tool results."
	}
	_, _ = l.emit(EventModelResp, stepID, ModelRespPayload{
		Text:      text,
		ToolCalls: nil,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CostUSD:      resp.Usage.CostUSD,
		},
	})
	l.Messages = append(l.Messages, providers.Message{Role: "assistant", Content: text})
}

// execToolCall executes a single tool call and returns (denied, error).
// denied is true when a dangerous tool was denied by the confirm function.
func (l *Loop) execToolCall(ctx context.Context, stepID string, tc providers.ToolCall) (bool, error) {
	// Emit tool.call event
	_, err := l.emit(EventToolCall, stepID, ToolCallPayload{
		CallID: tc.ID,
		Name:   tc.Name,
		Args:   string(tc.Args),
	})
	if err != nil {
		return false, err
	}

	// Look up tool
	tool, ok := l.Tools.Get(tc.Name)
	if !ok {
		result := tools.ToolResult{OK: false, Output: fmt.Sprintf("tool %q not found or not enabled", tc.Name)}
		return false, l.emitToolResult(stepID, tc, result)
	}

	if toolRequiresNetworkGate(tool, l.Session) && !l.networkAllowed() {
		result := tools.ToolResult{
			OK:     false,
			Output: fmt.Sprintf("network access is disabled by sandbox policy (network_tier=%q)", l.effectiveNetworkTier()),
		}
		if err := l.emitToolResult(stepID, tc, result); err != nil {
			return false, err
		}
		l.Messages = append(l.Messages, providers.Message{
			Role:       "tool",
			Content:    "ERROR: " + result.Output,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		})
		return false, nil
	}

	// Confirm dangerous tools
	if tool.DangerLevel() == tools.Dangerous {
		// Optional reflection turn — burns an extra full-context model call per dangerous tool.
		// Only enabled when policy.ReflectOnDangerous is true.
		if l.Policy != nil && l.Policy.ReflectOnDangerous {
			confidence, uncertainty, err := l.reflectOnTool(ctx, stepID, tc)
			if err == nil {
				_, _ = l.emit(EventReflect, stepID, ToolReflectPayload{
					CallID:      tc.ID,
					Name:        tc.Name,
					Confidence:  confidence,
					Uncertainty: uncertainty,
				})

				if confidence < 0.5 {
					msg := "low confidence rejection (conf=" + fmt.Sprintf("%.2f", confidence) + "): " + uncertainty
					result := tools.ToolResult{OK: false, Output: msg}
					if err := l.emitToolResult(stepID, tc, result); err != nil {
						return false, err
					}
					l.Messages = append(l.Messages, providers.Message{
						Role:       "tool",
						Content:    "ERROR: " + msg,
						ToolCallID: tc.ID,
						Name:       tc.Name,
					})
					return false, nil
				}
			}
		}

		if l.ConfirmFn != nil && !l.ConfirmFn(tc.Name, string(tc.Args)) {
			result := tools.ToolResult{OK: false, Output: "user denied tool execution"}
			if err := l.emitToolResult(stepID, tc, result); err != nil {
				return false, err
			}
			// Add denial as tool message
			l.Messages = append(l.Messages, providers.Message{
				Role:       "tool",
				Content:    "user denied tool execution",
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
			return true, nil
		}
	}

	// Execute tool
	timeout := 30000
	if l.Policy != nil && l.Policy.ToolTimeoutMS > 0 {
		timeout = l.Policy.ToolTimeoutMS
	}
	if l.Snapshots != nil && tool.Effects().MutatesWorkspace {
		snap, err := l.snapshotManager().Capture(ctx, SnapshotRequest{
			RunID:    l.Run.ID,
			StepID:   stepID,
			CallID:   tc.ID,
			ToolName: tc.Name,
			Reason:   "before_mutating_tool",
		})
		if err != nil {
			return false, fmt.Errorf("capture snapshot before tool %q: %w", tc.Name, err)
		}
		if _, err := l.emit(EventSandboxSnapshot, stepID, SandboxSnapshotPayload{
			SnapshotID: snap.ID,
			CallID:     tc.ID,
			Name:       tc.Name,
			Method:     snap.Method,
			Reason:     "before_mutating_tool",
		}); err != nil {
			return false, err
		}
	}
	var deltaMu sync.Mutex
	hostWorkspaceDir := l.Run.Dir
	if l.Mapper != nil && strings.TrimSpace(l.Mapper.HostRoot) != "" {
		hostWorkspaceDir = l.Mapper.HostRoot
	}
	callCtx := tools.ToolCallContext{
		RunID:            l.Run.ID,
		StepID:           stepID,
		CallID:           tc.ID,
		WorkspaceDir:     l.Run.Dir,
		HostWorkspaceDir: hostWorkspaceDir,
		TimeoutMS:        timeout,
		Provider:         l.Provider,
		Registry:         l.Tools,
		Session:          l.Session,
		Mapper:           l.Mapper,
		EmitOutputDelta: func(stream, text string) error {
			if strings.TrimSpace(text) == "" {
				return nil
			}
			if l.Mapper != nil {
				text = l.Mapper.SanitizeText(text)
			}
			deltaMu.Lock()
			defer deltaMu.Unlock()
			_, err := l.emit(EventToolOutputDelta, stepID, ToolOutputDeltaPayload{
				CallID: tc.ID,
				Name:   tc.Name,
				Stream: stream,
				Delta:  text,
			})
			return err
		},
	}

	result, err := tool.Exec(ctx, callCtx, tc.Args)
	if err != nil {
		result = tools.ToolResult{OK: false, Output: "tool exec error: " + err.Error()}
	}

	if err := l.emitToolResult(stepID, tc, result); err != nil {
		return false, err
	}

	// Add tool result to message history
	content := result.Output
	if !result.OK {
		content = "ERROR: " + result.Output
	}
	// Layer 0: provider-aware inline truncation of oversized tool results.
	if maxChars := l.toolResultCharLimit(stepID); maxChars > 0 {
		content = TruncateToolResult(content, maxChars)
	}
	// Fix #1: Sanitize host paths in tool results to prevent double-prepend bug
	if l.Mapper != nil {
		content = l.Mapper.SanitizeText(content)
	}
	l.Messages = append(l.Messages, providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: tc.ID,
		Name:       tc.Name,
	})

	// Feedback Loop: Auto-verify build if tool mutated workspace
	if result.OK && tool.Effects().MutatesWorkspace {
		// verifyBuild handles its own event emission and message injection.
		// Ignore the returned error so the loop can continue with the injected alert context.
		_ = l.verifyBuild(ctx, stepID)
	}

	return false, nil
}

func toolRequiresNetworkGate(tool tools.Tool, session executor.Session) bool {
	if tool == nil {
		return false
	}
	if !tool.Effects().NeedsNetwork {
		return false
	}
	// Docker network mode already enforces shell command connectivity. In host-mode
	// sandbox, allowing shell through this gate would also allow arbitrary network
	// access via curl/wget/etc because commands run directly on the host.
	if tool.Name() == "sh" && session != nil && session.Type() == "docker" {
		return false
	}
	return true
}

// verifyBuild runs a background build check and injects an alert if it fails.
func (l *Loop) verifyBuild(ctx context.Context, stepID string) error {
	// Only run if we have a shell tool available or can run commands
	sh, ok := l.Tools.Get("sh")
	if !ok {
		return nil
	}

	// Run go build ./...
	args, _ := json.Marshal(map[string]string{"cmd": "go build ./..."})
	res, err := sh.Exec(ctx, tools.ToolCallContext{
		RunID:        l.Run.ID,
		StepID:       stepID,
		WorkspaceDir: l.Run.Dir,
		Mapper:       l.Mapper,
	}, args)

	if err == nil && !res.OK {
		alert := "SYSTEM ALERT: Your last change caused a compilation error:\n\n" + res.Output +
			"\n\nPlease fix these errors before proceeding."
		l.Messages = append(l.Messages, providers.Message{
			Role:    "system",
			Content: alert,
		})
		// We could emit a specific event here if we had one, for now just log/inject
	}
	return nil
}

func (l *Loop) networkAllowed() bool {
	switch l.effectiveNetworkTier() {
	case "open", "research":
		return true
	default:
		return false
	}
}

func (l *Loop) effectiveNetworkTier() string {
	tier := strings.ToLower(strings.TrimSpace(l.NetworkTier))
	if tier == "" {
		return "open"
	}
	return tier
}

func (l *Loop) snapshotManager() SnapshotManager {
	if l.Snapshots != nil {
		return l.Snapshots
	}
	return NoopSnapshotManager{}
}

func (l *Loop) emitToolResult(stepID string, tc providers.ToolCall, result tools.ToolResult) error {
	l.lastToolOK = result.OK
	l.lastToolOutput = result.Output
	l.lastToolName = tc.Name
	l.lastToolArgs = string(tc.Args)

	_, err := l.emit(EventToolResult, stepID, ToolResultPayload{
		CallID:     tc.ID,
		Name:       tc.Name,
		OK:         result.OK,
		Output:     result.Output,
		DurationMS: result.DurationMS,
	})

	// Detect if the output contains a raw PNG image and emit it for inline terminal rendering.
	rawOut := result.Stdout
	if rawOut == "" {
		rawOut = result.Output
	}
	if result.OK && (strings.HasPrefix(rawOut, "\x89PNG\r\n\x1a\n") || strings.HasPrefix(rawOut, "\x89PNG")) {
		b64 := base64.StdEncoding.EncodeToString([]byte(rawOut))
		_, _ = l.emit(EventImageInline, stepID, ImageInlinePayload{
			Data:  b64,
			Index: 0,
		})
	}

	return err
}

func (l *Loop) reflectOnTool(ctx context.Context, stepID string, tc providers.ToolCall) (float64, string, error) {
	prompt := fmt.Sprintf("You are about to execute the tool %q with arguments: %s\n\n"+
		"On a scale of 0.0 to 1.0, what is your confidence that this is the correct next step to achieve the goal? "+
		"If below 0.7, please state your primary uncertainty concisely.\n\n"+
		"Respond ONLY in JSON format: {\"confidence\": 0.XX, \"uncertainty\": \"...\"}",
		tc.Name, string(tc.Args))

	msgs := append([]providers.Message{}, l.buildMessages(false)...)
	msgs = append(msgs, providers.Message{Role: "user", Content: prompt})

	resp, err := l.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    l.Run.ID,
		StepID:   stepID,
		Messages: msgs,
		Hints:    providers.Hints{JSONOnly: true},
	})
	if err != nil {
		return 0, "", err
	}

	var res struct {
		Confidence  float64 `json:"confidence"`
		Uncertainty string  `json:"uncertainty"`
	}
	if err := json.Unmarshal([]byte(resp.AssistantText), &res); err != nil {
		return 0.8, "failed to parse reflection", nil
	}

	return res.Confidence, res.Uncertainty, nil
}

func (l *Loop) buildMessages(includeMemory bool) []providers.Message {
	return l.buildMessagesWithStepMemory("", includeMemory, false)
}

func (l *Loop) buildMessagesForStep(stepID string) []providers.Message {
	return l.buildMessagesWithStepMemory(stepID, true, true)
}

func (l *Loop) previewMessagesForStep(stepID string) []providers.Message {
	return l.buildMessagesWithStepMemory(stepID, true, false)
}

func (l *Loop) buildMessagesWithStepMemory(stepID string, includeMemory bool, consumeMemory bool) []providers.Message {
	msgs := make([]providers.Message, 0, len(l.Messages)+2)

	// 1. Static system prompt
	if l.Policy != nil && l.Policy.SystemPrompt != "" {
		msgs = append(msgs, providers.Message{
			Role:    "system",
			Content: l.Policy.SystemPrompt,
		})
	}

	// 2. Dynamic persistent memory — re-read when needed so in-run writes are visible.
	if includeMemory {
		if memMsg, ok := l.memoryReferenceMessageForStep(stepID, consumeMemory); ok {
			msgs = append(msgs, providers.Message{
				Role:    "assistant",
				Content: memMsg,
			})
		}
	}

	// 3. Conversation history
	msgs = append(msgs, l.Messages...)
	return msgs
}

// SanitizeLiveMessages removes unresolved assistant tool calls from l.Messages
// before the next provider request. This prevents MiniMax error 2013 when
// a long-running tool call hasn't completed before a new step is processed.
// Returns true if any tool calls were removed.
func (l *Loop) SanitizeLiveMessages() bool {
	toolResults := make(map[string]bool)
	for _, msg := range l.Messages {
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolCallID) != "" {
			toolResults[msg.ToolCallID] = true
		}
	}

	modified := false
	out := make([]providers.Message, 0, len(l.Messages))
	for _, msg := range l.Messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}
		// Filter to keep only tool calls that have results
		filtered := make([]providers.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if strings.TrimSpace(tc.ID) != "" && toolResults[tc.ID] {
				filtered = append(filtered, tc)
			} else {
				modified = true
			}
		}
		// If no resolved tool calls remain but there's text content, keep the message
		if len(filtered) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				// No text and no resolved tool calls: skip this message
				modified = true
				continue
			}
			// Keep message but without tool calls
			msg.ToolCalls = nil
			out = append(out, msg)
		} else {
			msg.ToolCalls = filtered
			out = append(out, msg)
		}
	}
	if modified {
		l.Messages = out
	}
	return modified
}

func (l *Loop) memoryReferenceMessageForStep(stepID string, consume bool) (string, bool) {
	if stepID == "" {
		return l.memoryReferenceMessage()
	}
	l.prepareMemoryForStep(stepID)
	if !l.memoryStepEligible {
		return "", false
	}
	if consume && l.memoryStepConsumed {
		return "", false
	}
	if consume {
		l.memoryStepConsumed = true
	}
	return l.memoryStepMessage, true
}

func (l *Loop) prepareMemoryForStep(stepID string) {
	if l.memoryStepID == stepID {
		return
	}
	msg, ok := l.memoryReferenceMessage()
	l.memoryStepID = stepID
	l.memoryStepMessage = msg
	l.memoryStepEligible = ok
	l.memoryStepConsumed = false
}

func (l *Loop) shouldIncludeMemory() bool {
	if l.Policy == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(l.Policy.MemoryMode)) {
	case "", "auto":
		return memoryLooksRelevant(latestUserMessage(l.Messages))
	case "always":
		return true
	case "off":
		return false
	default:
		return memoryLooksRelevant(latestUserMessage(l.Messages))
	}
}

func (l *Loop) memoryReferenceTokenBudget() int {
	if l.Policy != nil && l.Policy.MemoryMaxTokens > 0 {
		return l.Policy.MemoryMaxTokens
	}
	return defaultMemoryReferenceTokenBudget
}

func latestUserMessage(msgs []providers.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func memoryLooksRelevant(input string) bool {
	input = strings.ToLower(input)
	if strings.TrimSpace(input) == "" {
		return false
	}
	keywords := []string{
		"remember", "memory", "previous", "earlier", "before", "prior",
		"history", "context", "continue", "resume", "recap", "again",
		"last time", "we decided", "we agreed", "convention", "decision",
	}
	for _, kw := range keywords {
		if strings.Contains(input, kw) {
			return true
		}
	}
	return false
}

// compressProvider returns the provider to use for compression calls.
func (l *Loop) compressProvider() providers.Provider {
	if l.CompressProvider != nil {
		return l.CompressProvider
	}
	return l.Provider
}

// TruncateToolResult applies head+tail truncation to oversized tool results.
// If maxChars <= 0 or len(content) <= maxChars, content is returned as-is.
func TruncateToolResult(content string, maxChars int) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	keep := maxChars * 2 / 5
	head := content[:keep]
	tail := content[len(content)-keep:]
	trimmed := len(content) - 2*keep
	return head + fmt.Sprintf("\n\n[... truncated %d chars ...]\n\n", trimmed) + tail
}

func (l *Loop) toolResultCharLimit(stepID string) int {
	limit := 0
	if l.Policy != nil && l.Policy.MaxToolResultChars > 0 {
		limit = l.Policy.MaxToolResultChars
	}

	threshold := l.providerAwareEvidenceThreshold()
	if threshold <= 0 {
		return limit
	}

	currentTokens := estimateTokens(l.previewMessagesForStep(stepID))
	remaining := threshold - currentTokens

	// Keep headroom for at least one more model turn plus surrounding framing.
	reserve := threshold / 10
	if reserve < 1024 {
		reserve = 1024
	}
	if reserve > 8192 {
		reserve = 8192
	}

	tokenBudget := remaining - reserve
	dynamicLimit := 400 // always preserve enough signal for head+tail truncation
	if tokenBudget > 0 {
		// estimateTokensSingle uses ~3.3 chars/token; round down conservatively.
		dynamicLimit = tokenBudget * 3
		if dynamicLimit < 400 {
			dynamicLimit = 400
		}
	}

	if limit == 0 || dynamicLimit < limit {
		return dynamicLimit
	}
	return limit
}

func (l *Loop) providerAwareEvidenceThreshold() int {
	if l.Policy != nil && l.Policy.ContextLimit > 0 {
		return l.Policy.ContextLimit * 3 / 4
	}
	if l.ModelMetadata.ContextSize > 0 {
		return l.ModelMetadata.ContextSize * 3 / 4
	}
	return 0
}

// estimateTokensSingle returns the estimated token count for a single message.
func estimateTokensSingle(m providers.Message) int {
	n := 4 // per-message framing (role markers, separators)
	n += len(m.Content)*10/33 + 1
	for _, tc := range m.ToolCalls {
		n += 10 // tool call framing (id, name, type fields)
		n += len(tc.Args)*10/33 + 1
	}
	return n
}

// estimateTokens returns an estimated token count for a message slice.
// Uses ~3.3 chars/token (more accurate than len/4 for mixed code/text) plus
// per-message framing overhead and tool call structure tokens.
func estimateTokens(msgs []providers.Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateTokensSingle(m)
	}
	return n
}

func targetedCompressionLimit(p providers.Provider) int {
	if p == nil {
		return 0
	}
	switch p.Name() {
	case "glm":
		// GLM is more likely to reject compression inputs and can hit request
		// limits quickly when targeted compression fans out into many calls.
		// Prefer a single bulk summary call instead.
		return 0
	default:
		// Keep targeted compression bounded so one overloaded step cannot
		// explode into a long burst of extra provider requests.
		return 3
	}
}

var compressionMarkerReplacer = strings.NewReplacer(
	"<arg_key>", " ",
	"</arg_key>", " ",
	"<arg_value>", " ",
	"</arg_value>", " ",
)

func sanitizeCompressionContent(content string) string {
	if content == "" {
		return content
	}
	content = compressionMarkerReplacer.Replace(content)
	content = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20:
			return -1
		default:
			return r
		}
	}, content)
	content = strings.Join(strings.Fields(content), " ")

	const maxCompressionChars = 12000
	if len(content) <= maxCompressionChars {
		return content
	}
	keep := maxCompressionChars * 2 / 5
	head := content[:keep]
	tail := content[len(content)-keep:]
	return head + fmt.Sprintf(" [... truncated %d chars ...] ", len(content)-(2*keep)) + tail
}

func sanitizeCompressionMessages(msgs []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(msgs))
	for _, m := range msgs {
		cp := m
		cp.Content = sanitizeCompressionContent(m.Content)
		out = append(out, cp)
	}
	return out
}

// maybeCompress implements a two-pass compression strategy:
//   - Pass 1 (targeted): compress the N largest non-recent messages individually
//   - Pass 2 (bulk): fall back to oldest-half summarization if still over threshold
func (l *Loop) maybeCompress(ctx context.Context, stepID string) error {
	tokensBefore := estimateTokens(l.previewMessagesForStep(stepID))
	trigger := l.compressionTrigger(tokensBefore)
	if trigger == "" {
		return nil
	}

	msgsBefore := len(l.Messages)
	startTime := time.Now()

	// ── Pass 1: Targeted per-message compression ──────────────────────────
	protectRecent := 6
	if l.Policy != nil && l.Policy.CompressProtectRecent > 0 {
		protectRecent = l.Policy.CompressProtectRecent
	}

	compressible := len(l.Messages) - protectRecent
	if compressible < 1 {
		compressible = 0
	}

	// Find large non-protected messages
	type candidate struct {
		idx    int
		tokens int
	}
	var candidates []candidate
	for i := 0; i < compressible; i++ {
		t := estimateTokensSingle(l.Messages[i])
		if t > 500 {
			candidates = append(candidates, candidate{idx: i, tokens: t})
		}
	}

	// Sort by token count descending
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].tokens > candidates[b].tokens
	})

	var totalCompressCost float64
	compressed := 0
	failedCount := 0
	cp := l.compressProvider()
	targetedLimit := targetedCompressionLimit(cp)

	if targetedLimit > 0 {
		for i, c := range candidates {
			if i >= targetedLimit {
				break
			}
			m := l.Messages[c.idx]
			summaryReq := providers.CompleteRequest{
				RunID: l.Run.ID,
				Messages: []providers.Message{
					{
						Role:    "system",
						Content: "Summarize the following message content in a dense, compact form. Preserve key facts, file paths, decisions, and results. Remove verbatim content and repetition. Be very concise.",
					},
					{
						Role:    "user",
						Content: sanitizeCompressionContent(m.Content),
					},
				},
			}
			resp, err := cp.Complete(ctx, summaryReq)
			if err != nil {
				failedCount++
				_, _ = l.emit(EventRunError, stepID, RunErrorPayload{
					Error: fmt.Sprintf("compress: failed to compress message %d (skipping): %v", c.idx, err),
				})
				fmt.Fprintf(os.Stderr, "warning: compression failed for message %d: %v\n", c.idx, err)
				continue // skip this message, try next
			}

			if err := l.Budget.AddTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens); err != nil {
				fmt.Fprintf(os.Stderr, "warning: budget exceeded adding compression tokens: %v\n", err)
			}
			if err := l.Budget.AddCost(resp.Usage.CostUSD); err != nil {
				fmt.Fprintf(os.Stderr, "warning: budget exceeded adding compression cost: %v\n", err)
			}
			totalCompressCost += resp.Usage.CostUSD

			// Replace content in-place, preserving metadata
			l.Messages[c.idx].Content = "[compressed] " + resp.AssistantText
			compressed++

			// Stop early once pressure has eased enough.
			if l.compressionTrigger(estimateTokens(l.previewMessagesForStep(stepID))) == "" {
				break
			}
		}
	}

	if compressed > 0 || failedCount > 0 {
		tokensAfter := estimateTokens(l.previewMessagesForStep(stepID))
		_, _ = l.emit(EventCompress, stepID, CompressPayload{
			MessagesBefore:     msgsBefore,
			MessagesAfter:      len(l.Messages),
			TokensBefore:       tokensBefore,
			TokensAfter:        tokensAfter,
			CostUSD:            totalCompressCost,
			Trigger:            trigger,
			Strategy:           "targeted",
			MessagesCompressed: compressed,
			MessagesFailed:     failedCount,
			TokensSaved:        tokensBefore - tokensAfter,
			DurationMS:         time.Since(startTime).Milliseconds(),
			ProviderModel:      cp.Name(),
		})

		nextTrigger := l.compressionTrigger(tokensAfter)
		if nextTrigger == "" {
			return nil
		}
		// Update tokensBefore for pass 2
		tokensBefore = tokensAfter
		trigger = nextTrigger
	}

	// ── Pass 2: Bulk fallback (oldest-half summarization) ─────────────────
	cutoff := len(l.Messages) / 2
	if cutoff < 4 {
		return nil // too short to compress meaningfully
	}
	toSummarize := l.Messages[:cutoff]

	summaryReq := providers.CompleteRequest{
		RunID: l.Run.ID,
		Messages: append(
			[]providers.Message{{
				Role:    "system",
				Content: "You are a summarizer. Produce a dense, structured summary of the following conversation segment. Preserve: decisions made, files read/edited, tool results, current task state. Be concise.",
			}},
			sanitizeCompressionMessages(toSummarize)...,
		),
	}
	resp, err := cp.Complete(ctx, summaryReq)
	if err != nil {
		return err
	}
	if err := l.Budget.AddTokens(resp.Usage.InputTokens, resp.Usage.OutputTokens); err != nil {
		fmt.Fprintf(os.Stderr, "warning: budget exceeded adding compression tokens: %v\n", err)
	}
	if err := l.Budget.AddCost(resp.Usage.CostUSD); err != nil {
		fmt.Fprintf(os.Stderr, "warning: budget exceeded adding compression cost: %v\n", err)
	}

	summary := providers.Message{
		Role:    "system",
		Content: "[CONTEXT SUMMARY — earlier conversation compressed]\n\n" + resp.AssistantText,
	}
	l.Messages = append([]providers.Message{summary}, l.Messages[cutoff:]...)

	tokensAfter := estimateTokens(l.previewMessagesForStep(stepID))
	_, _ = l.emit(EventCompress, stepID, CompressPayload{
		MessagesBefore:     msgsBefore,
		MessagesAfter:      len(l.Messages),
		TokensBefore:       tokensBefore,
		TokensAfter:        tokensAfter,
		CostUSD:            resp.Usage.CostUSD,
		Trigger:            trigger,
		Strategy:           "bulk",
		MessagesCompressed: cutoff,
		TokensSaved:        tokensBefore - tokensAfter,
		DurationMS:         time.Since(startTime).Milliseconds(),
		ProviderModel:      cp.Name(),
	})
	return nil
}

func (l *Loop) compressionTrigger(tokensBefore int) string {
	if l.Policy != nil && l.Policy.ContextLimit > 0 {
		threshold := l.Policy.ContextLimit * 3 / 4
		if tokensBefore >= threshold {
			return "context_limit"
		}
	}
	if l.Budget == nil || tokensBefore < budgetCompressionMinContextTokens {
		return ""
	}
	b := l.Budget.Budget()
	if b.MaxTokens <= 0 {
		return ""
	}
	remaining := b.MaxTokens - b.UsedTokens
	if remaining <= 0 {
		return ""
	}
	if remaining <= tokensBefore*budgetCompressionLookaheadSteps {
		return "budget_tokens"
	}
	return ""
}

func (l *Loop) emit(t EventType, stepID string, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("emit marshal: %w", err)
	}
	ev := Event{
		TS:      time.Now().UTC(),
		RunID:   l.Run.ID,
		StepID:  stepID,
		EventID: newID(),
		Type:    t,
		Payload: b,
	}
	if err := l.Trace.Write(ev); err != nil {
		return ev, fmt.Errorf("trace write: %w", err)
	}
	if l.OutputFn != nil {
		l.OutputFn(ev)
	}
	return ev, nil
}

// EmitRunStart records the run.start event.
func (l *Loop) EmitRunStart(payload RunStartPayload) error {
	_, err := l.emit(EventRunStart, "", payload)
	return err
}

// EmitRunError records a run error event.
func (l *Loop) EmitRunError(stepID, message string) error {
	_, err := l.emit(EventRunError, stepID, RunErrorPayload{Error: message})
	return err
}

// EmitRunEnd records the run.end event.
func (l *Loop) EmitRunEnd(reason, summary string) error {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return nil
	}
	l.ended = true
	l.mu.Unlock()

	b := l.Budget.Budget()
	_, err := l.emit(EventRunEnd, "", RunEndPayload{
		Reason:     reason,
		UsedSteps:  b.UsedSteps,
		UsedTokens: b.UsedTokens,
		Summary:    summary,
	})
	return err
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ThresholdHook returns a hook that terminates the run after N consecutive tool failures.
func ThresholdHook(maxFailures int) PolicyHook {
	consecutiveFailures := 0
	return func(state LoopState) HookResult {
		if state.Stage != HookStageToolResult {
			return HookResult{Action: HookContinue}
		}
		if state.LastToolOK {
			consecutiveFailures = 0
			return HookResult{Action: HookContinue}
		}
		consecutiveFailures++
		if consecutiveFailures >= maxFailures {
			return HookResult{
				Action: HookTerminate,
				Reason: fmt.Sprintf("threshold reached: %d consecutive tool failures", maxFailures),
			}
		}
		return HookResult{Action: HookContinue}
	}
}

// LogHook returns a hook that logs loop state to a file for external monitoring.
func LogHook(w io.Writer) PolicyHook {
	return func(state LoopState) HookResult {
		b, _ := json.Marshal(state)
		_, _ = w.Write(append(b, '\n'))
		return HookResult{Action: HookContinue}
	}
}

// DeduplicationHook returns a hook that injects a warning when the agent
// repeats an identical tool call (same name + args) within a single step.
// After maxRepeats identical calls, it injects a system message telling the
// agent to use the existing result instead of re-calling.
func DeduplicationHook(maxRepeats int) PolicyHook {
	seen := make(map[string]int)
	lastStep := -1
	return func(state LoopState) HookResult {
		if state.Stage != HookStageToolResult {
			return HookResult{Action: HookContinue}
		}
		if state.LastToolName == "" {
			return HookResult{Action: HookContinue}
		}
		// Reset seen map on new step.
		if state.StepCount != lastStep {
			seen = make(map[string]int)
			lastStep = state.StepCount
		}
		key := state.LastToolName + "\x00" + state.LastToolArgs
		seen[key]++
		if seen[key] >= maxRepeats {
			output := state.LastToolOutput
			if len(output) > 200 {
				output = output[:200] + "..."
			}
			return HookResult{
				Action:  HookInjectMessage,
				Message: fmt.Sprintf("DEDUPLICATION WARNING: You already called %s with identical arguments %d times this step. Use the existing result instead of re-calling. Previous output: %s", state.LastToolName, seen[key], output),
			}
		}
		return HookResult{Action: HookContinue}
	}
}
