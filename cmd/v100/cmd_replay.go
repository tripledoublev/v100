package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/ui"
)

func replayCmd(cfgPath *string) *cobra.Command {
	var deterministic bool
	var stepMode bool
	var replaceModel string
	var injectTool []string
	var useTUI bool

	cmd := &cobra.Command{
		Use:   "replay <run_id>",
		Short: "Pretty-print a run trace as a readable transcript",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateReplayFlags(deterministic, stepMode, strings.TrimSpace(replaceModel), injectTool, useTUI); err != nil {
				return err
			}

			runID := args[0]
			runDir, err := findRunDir(runID)
			if err != nil {
				return err
			}

			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}

			if deterministic {
				cfg, err := loadConfig(*cfgPath)
				if err != nil {
					return err
				}
				injected, err := parseInjectedToolOutputs(injectTool)
				if err != nil {
					return err
				}
				return deterministicReplay(cmd.Context(), cfg, events, deterministicReplayOptions{
					StepMode:     stepMode,
					ReplaceModel: strings.TrimSpace(replaceModel),
					InjectTools:  injected,
				})
			}

			if useTUI {
				m := ui.NewScrubModel(runID, events)
				p := tea.NewProgram(m, tea.WithAltScreen())
				_, err := p.Run()
				return err
			}

			for _, ev := range events {
				ui.PrintReplayEvent(ev)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&deterministic, "deterministic", false, "replay recorded model/tool events deterministically")
	cmd.Flags().BoolVar(&stepMode, "step", false, "pause between deterministic replay events")
	cmd.Flags().StringVar(&replaceModel, "replace-model", "", "run recorded model.call events against this model and show counterfactual responses")
	cmd.Flags().StringArrayVar(&injectTool, "inject-tool", nil, "inject tool output during deterministic replay: name=output (repeatable)")
	cmd.Flags().BoolVar(&useTUI, "tui", false, "launch interactive trace scrubber")
	return cmd
}

func validateReplayFlags(deterministic, stepMode bool, replaceModel string, injectTool []string, useTUI bool) error {
	if useTUI {
		if deterministic || stepMode || replaceModel != "" || len(injectTool) > 0 {
			return fmt.Errorf("--tui cannot be combined with deterministic replay flags")
		}
		return nil
	}
	if !deterministic {
		if stepMode {
			return fmt.Errorf("--step requires --deterministic")
		}
		if replaceModel != "" {
			return fmt.Errorf("--replace-model requires --deterministic")
		}
		if len(injectTool) > 0 {
			return fmt.Errorf("--inject-tool requires --deterministic")
		}
	}
	return nil
}

type deterministicReplayOptions struct {
	StepMode     bool
	ReplaceModel string
	InjectTools  map[string]string
}

func deterministicReplay(ctx context.Context, cfg *config.Config, events []core.Event, opts deterministicReplayOptions) error {
	fmt.Println(ui.Header("Deterministic Replay"))
	fmt.Println(ui.Dim("model/tool outputs are sourced from trace records only"))
	if opts.ReplaceModel != "" {
		fmt.Println(ui.Info("counterfactual model override: " + opts.ReplaceModel))
	}
	if len(opts.InjectTools) > 0 {
		fmt.Println(ui.Info(fmt.Sprintf("tool injections active: %d", len(opts.InjectTools))))
	}

	hasModelCall := false
	reader := bufio.NewReader(os.Stdin)
	var prov providers.Provider
	var providerName string
	var counterfactualQ []providers.CompleteResponse
	var counterfactualErrQ []error

	pause := func(label string) error {
		if !opts.StepMode {
			return nil
		}
		fmt.Print(ui.Dim("Press Enter to continue (" + label + ")..."))
		_, err := reader.ReadString('\n')
		return err
	}

	for i, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if strings.TrimSpace(p.Provider) != "" {
				providerName = p.Provider
			}
			ui.PrintReplayEvent(ev)

		case core.EventModelCall:
			hasModelCall = true
			var p core.ModelCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] MODEL CALL  step=%s", i+1, shortID(ev.StepID))))
			fmt.Printf("%s\n", ui.Dim(fmt.Sprintf("tools=%d  max_tool_calls=%d", len(p.ToolNames), p.MaxToolCalls)))
			msgs := applyInjectedToolOutputs(p.Messages, opts.InjectTools)
			for _, m := range msgs {
				content := strings.TrimSpace(m.Content)
				if len(content) > 200 {
					content = content[:200] + "…"
				}
				content = strings.ReplaceAll(content, "\n", " ")
				if content == "" && len(m.ToolCalls) > 0 {
					content = fmt.Sprintf("[assistant tool-calls: %d]", len(m.ToolCalls))
				}
				fmt.Printf("  %s: %s\n", m.Role, content)
			}

			if opts.ReplaceModel != "" {
				if prov == nil {
					pn := providerName
					if strings.TrimSpace(pn) == "" {
						pn = cfg.Defaults.Provider
					}
					var err error
					prov, err = buildProvider(cfg, pn)
					if err != nil {
						counterfactualQ = append(counterfactualQ, providers.CompleteResponse{})
						counterfactualErrQ = append(counterfactualErrQ, err)
					}
				}
				if prov != nil {
					toolSpecs := replayToolSpecs(cfg, p.ToolNames)
					resp, err := prov.Complete(ctx, providers.CompleteRequest{
						RunID:    ev.RunID,
						StepID:   ev.StepID,
						Messages: msgs,
						Tools:    toolSpecs,
						Model:    opts.ReplaceModel,
						Hints: providers.Hints{
							MaxToolCalls: p.MaxToolCalls,
						},
					})
					counterfactualQ = append(counterfactualQ, resp)
					counterfactualErrQ = append(counterfactualErrQ, err)
				}
			}
			if err := pause("model.call"); err != nil {
				return err
			}

		case core.EventModelResp:
			var p core.ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] MODEL RESPONSE (recorded)", i+1)))
			if strings.TrimSpace(p.Text) != "" {
				fmt.Println(p.Text)
			}
			if len(p.ToolCalls) > 0 {
				fmt.Println(ui.Dim(fmt.Sprintf("tool_calls=%d", len(p.ToolCalls))))
			}
			if opts.ReplaceModel != "" && len(counterfactualQ) > 0 && len(counterfactualErrQ) > 0 {
				cf := counterfactualQ[0]
				cerr := counterfactualErrQ[0]
				counterfactualQ = counterfactualQ[1:]
				counterfactualErrQ = counterfactualErrQ[1:]
				fmt.Printf("\n%s\n", styleReplayTitle("COUNTERFACTUAL RESPONSE"))
				if cerr != nil {
					fmt.Println(ui.Fail("counterfactual model call failed: " + cerr.Error()))
				} else {
					if strings.TrimSpace(cf.AssistantText) != "" {
						fmt.Println(cf.AssistantText)
					} else {
						fmt.Println(ui.Dim("(empty assistant text)"))
					}
					if len(cf.ToolCalls) > 0 {
						fmt.Println(ui.Dim(fmt.Sprintf("counterfactual tool_calls=%d", len(cf.ToolCalls))))
					}
				}
			}
			if err := pause("model.response"); err != nil {
				return err
			}

		case core.EventToolCall:
			var p core.ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] TOOL CALL (recorded) %s", i+1, p.Name)))
			fmt.Println(p.Args)
			if err := pause("tool.call"); err != nil {
				return err
			}

		case core.EventToolResult:
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			fmt.Printf("\n%s\n", styleReplayTitle(fmt.Sprintf("[%d] TOOL RESULT (recorded) %s  ok=%t", i+1, p.Name, p.OK)))
			out := p.Output
			if inj, ok := opts.InjectTools[p.Name]; ok {
				out = inj
				fmt.Println(ui.Warn("injected tool output override applied"))
			}
			if len(out) > 500 {
				out = out[:500] + "…"
			}
			fmt.Println(out)
			if err := pause("tool.result"); err != nil {
				return err
			}

		default:
			// Keep compatibility with all existing event types.
			ui.PrintReplayEvent(ev)
		}
	}

	if !hasModelCall {
		fmt.Println(ui.Warn("trace has no model.call events; rerun with newer v100 to capture deterministic prompts"))
	}
	return nil
}

func replayToolSpecs(cfg *config.Config, names []string) []providers.ToolSpec {
	if len(names) == 0 {
		return nil
	}
	reg := buildToolRegistry(cfg)
	out := make([]providers.ToolSpec, 0, len(names))
	for _, n := range names {
		t, ok := reg.Get(n)
		if !ok {
			continue
		}
		out = append(out, providers.ToolSpec{
			Name:         t.Name(),
			Description:  t.Description(),
			InputSchema:  t.InputSchema(),
			OutputSchema: t.OutputSchema(),
		})
	}
	return out
}

func parseInjectedToolOutputs(raw []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range raw {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --inject-tool %q (want name=output)", v)
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			return nil, fmt.Errorf("invalid --inject-tool %q (empty tool name)", v)
		}
		m[name] = parts[1]
	}
	return m, nil
}

func applyInjectedToolOutputs(msgs []providers.Message, injected map[string]string) []providers.Message {
	if len(injected) == 0 {
		return msgs
	}
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		if v, ok := injected[out[i].Name]; ok {
			out[i].Content = v
		}
	}
	return out
}

func styleReplayTitle(s string) string {
	return ui.Bold(s)
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
