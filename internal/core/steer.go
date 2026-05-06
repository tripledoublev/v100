package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SteerHook polls the active trace for externally appended steer messages and
// injects each unseen correction into the next model turn.
func SteerHook(tracePath string) PolicyHook {
	seen := map[string]bool{}
	return func(state LoopState) HookResult {
		if strings.TrimSpace(tracePath) == "" {
			return HookResult{Action: HookContinue}
		}
		events, err := ReadAll(tracePath)
		if err != nil {
			return HookResult{Action: HookContinue}
		}
		for _, ev := range events {
			if ev.Type != EventUserMsg || seen[steerEventKey(ev)] {
				continue
			}
			var payload UserMsgPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				seen[steerEventKey(ev)] = true
				continue
			}
			if payload.Source != "steer" {
				continue
			}
			seen[steerEventKey(ev)] = true
			content := strings.TrimSpace(payload.Content)
			if content == "" {
				continue
			}
			return HookResult{
				Action:  HookInjectMessage,
				Message: fmt.Sprintf("Operator steering update: %s", content),
				Reason:  "external_steer",
			}
		}
		return HookResult{Action: HookContinue}
	}
}

func steerEventKey(ev Event) string {
	if ev.EventID != "" {
		return ev.EventID
	}
	return ev.TS.Format("20060102150405.000000000") + ":" + ev.StepID + ":" + string(ev.Payload)
}
