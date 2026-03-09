package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tripledoublev/v100/internal/core"
)

// ShareGPTMessage represents a single message in ShareGPT format.
type ShareGPTMessage struct {
	From  string `json:"from"`
	Value string `json:"value"`
}

// DistillTrace converts a JSONL trace to ShareGPT format.
// It reads events from the trace file and produces a conversation array
// with "human" and "gpt" message roles.
func DistillTrace(tracePath string) ([]ShareGPTMessage, error) {
	events, err := core.ReadAll(tracePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace: %w", err)
	}

	var conversation []ShareGPTMessage

	// Track the current assistant message being built
	var currentAssistant string
	hasAssistant := false

	for _, ev := range events {
		switch ev.Type {
		case core.EventUserMsg:
			var payload core.UserMsgPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			// Flush any pending assistant message first
			if hasAssistant {
				conversation = append(conversation, ShareGPTMessage{
					From:  "gpt",
					Value: currentAssistant,
				})
				currentAssistant = ""
				hasAssistant = false
			}
			conversation = append(conversation, ShareGPTMessage{
				From:  "human",
				Value: payload.Content,
			})

		case core.EventModelResp:
			var payload core.ModelRespPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			// Append to current assistant message (may be streamed)
			currentAssistant += payload.Text
			hasAssistant = true

		case core.EventToolResult:
			// Tool results are appended as human messages with tool context
			var payload core.ToolResultPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			// Flush any pending assistant message first
			if hasAssistant {
				conversation = append(conversation, ShareGPTMessage{
					From:  "gpt",
					Value: currentAssistant,
				})
				currentAssistant = ""
				hasAssistant = false
			}
			// Add tool result as human message (conventional for ShareGPT)
			toolContent := fmt.Sprintf("[%s] %s", payload.Name, payload.Output)
			conversation = append(conversation, ShareGPTMessage{
				From:  "human",
				Value: toolContent,
			})

		case core.EventRunEnd:
			// Flush any pending assistant message at end of run
			if hasAssistant {
				conversation = append(conversation, ShareGPTMessage{
					From:  "gpt",
					Value: currentAssistant,
				})
			}
		}
	}

	return conversation, nil
}

// WriteShareGPT writes the conversation to a JSON file in ShareGPT format.
func WriteShareGPT(conversation []ShareGPTMessage, outputPath string) error {
	data, err := json.MarshalIndent(conversation, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	return os.WriteFile(outputPath, data, 0644)
}

// DistillRun converts a run's trace to ShareGPT format and writes to distill.json.
func DistillRun(runDir string) (string, error) {
	tracePath := filepath.Join(runDir, "trace.jsonl")
	if _, err := os.Stat(tracePath); err != nil {
		return "", fmt.Errorf("trace not found: %w", err)
	}

	conversation, err := DistillTrace(tracePath)
	if err != nil {
		return "", err
	}

	outputPath := filepath.Join(runDir, "distill.json")
	if err := WriteShareGPT(conversation, outputPath); err != nil {
		return "", err
	}

	return outputPath, nil
}
