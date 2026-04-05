package main

import (
	"strings"
	"testing"
)

func TestReplayCmdRegistersTUIFlag(t *testing.T) {
	cfgPath := ""
	cmd := replayCmd(&cfgPath)
	if flag := cmd.Flags().Lookup("tui"); flag == nil {
		t.Fatal("expected replay command to register --tui")
	}
}

func TestValidateReplayFlagsRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name          string
		deterministic bool
		stepMode      bool
		replaceModel  string
		injectTool    []string
		useTUI        bool
		wantErr       string
	}{
		{
			name:          "tui with deterministic",
			deterministic: true,
			useTUI:        true,
			wantErr:       "--tui cannot be combined",
		},
		{
			name:         "tui with replace model",
			useTUI:       true,
			wantErr:      "--tui cannot be combined",
			replaceModel: "gpt-5.4",
		},
		{
			name:     "step without deterministic",
			stepMode: true,
			wantErr:  "--step requires --deterministic",
		},
		{
			name:         "replace model without deterministic",
			replaceModel: "gpt-5.4",
			wantErr:      "--replace-model requires --deterministic",
		},
		{
			name:       "inject tool without deterministic",
			injectTool: []string{"fs_read=mock"},
			wantErr:    "--inject-tool requires --deterministic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReplayFlags(tc.deterministic, tc.stepMode, tc.replaceModel, tc.injectTool, tc.useTUI)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateReplayFlagsAllowsSupportedModes(t *testing.T) {
	cases := []struct {
		name          string
		deterministic bool
		stepMode      bool
		replaceModel  string
		injectTool    []string
		useTUI        bool
	}{
		{name: "plain replay"},
		{name: "tui replay", useTUI: true},
		{name: "deterministic replay", deterministic: true, stepMode: true},
		{name: "counterfactual replay", deterministic: true, replaceModel: "gpt-5.4"},
		{name: "deterministic injection", deterministic: true, injectTool: []string{"fs_read=mock"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateReplayFlags(tc.deterministic, tc.stepMode, tc.replaceModel, tc.injectTool, tc.useTUI); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
