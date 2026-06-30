package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
)

// Command is a normalized slash command extracted from a chat message.
type Command struct {
	Name string
	Arg  string
}

// ParseCommand extracts a transport-neutral slash command from text.
func ParseCommand(text string) (Command, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") || text == "/" {
		return Command{}, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, false
	}
	token := strings.TrimPrefix(fields[0], "/")
	if i := strings.Index(token, "@"); i >= 0 {
		token = token[:i]
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return Command{}, false
	}
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	}
	return Command{Name: token, Arg: arg}, true
}

// CommandAllowed reports whether profile permits command. Missing profiles
// preserve legacy gateway behavior and allow local commands.
func CommandAllowed(profile config.GatewayProfile, hasProfile bool, command string) bool {
	if !hasProfile {
		return true
	}
	if len(profile.AllowedCommands) == 0 {
		return false
	}
	command = NormalizeCommandName(command)
	for _, allowed := range profile.AllowedCommands {
		if NormalizeCommandName(allowed) == command {
			return true
		}
	}
	return false
}

// NormalizeCommandName returns the lowercase bare command name without a slash.
func NormalizeCommandName(command string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(command)), "/")
}

// ProfileRuntime describes a resolved gateway profile.
type ProfileRuntime struct {
	Name    string
	Profile config.GatewayProfile
	OK      bool
}

// ResolveProfile selects the per-chat profile when set, otherwise the gateway default.
func ResolveProfile(profiles map[string]config.GatewayProfile, gatewayDefault string, chatProfiles map[string]string, chatID string) ProfileRuntime {
	profileName := strings.TrimSpace(gatewayDefault)
	if chatProfile := strings.TrimSpace(chatProfiles[chatID]); chatProfile != "" {
		profileName = chatProfile
	}
	if profileName == "" {
		return ProfileRuntime{}
	}
	profile, ok := profiles[profileName]
	return ProfileRuntime{Name: profileName, Profile: profile, OK: ok}
}

// ApplyProfileToSessionNew copies a profile's runtime, tool, prompt, network,
// and budget settings into ACP session/new params.
func ApplyProfileToSessionNew(params *acp.SessionNewParams, runtime ProfileRuntime, promptBaseDir string) error {
	if params == nil || (!runtime.OK && strings.TrimSpace(runtime.Name) == "") {
		return nil
	}
	if !runtime.OK {
		return fmt.Errorf("gateway profile %q not found", runtime.Name)
	}
	profile := runtime.Profile
	params.Provider = strings.TrimSpace(profile.Provider)
	params.Model = strings.TrimSpace(profile.Model)
	params.Solver = strings.TrimSpace(profile.Solver)
	params.Tools = CloneStringSlice(profile.Tools)
	params.Dangerous = CloneStringSlice(profile.Dangerous)
	params.SystemPrompt = strings.TrimSpace(profile.SystemPrompt)
	if promptPath := strings.TrimSpace(profile.SystemPromptPath); promptPath != "" {
		resolved := config.ResolvePromptFilePath(promptPath, promptBaseDir)
		data, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Errorf("read gateway profile %q system_prompt_path: %w", runtime.Name, err)
		}
		params.SystemPrompt = string(data)
	}
	params.NetworkTier = strings.TrimSpace(profile.NetworkTier)
	params.BudgetSteps = profile.BudgetSteps
	params.BudgetTokens = profile.BudgetTokens
	params.BudgetCostUSD = profile.BudgetCostUSD
	return nil
}

// ReconfigureParams builds ACP session/reconfigure params for a runtime command.
func ReconfigureParams(sessionID string, command Command) (acp.SessionReconfigureParams, bool) {
	value := strings.TrimSpace(command.Arg)
	if value == "" {
		return acp.SessionReconfigureParams{}, false
	}
	params := acp.SessionReconfigureParams{SessionID: strings.TrimSpace(sessionID)}
	switch NormalizeCommandName(command.Name) {
	case "provider":
		params.Provider = value
	case "model":
		params.Model = value
	case "solver":
		params.Solver = value
	default:
		return acp.SessionReconfigureParams{}, false
	}
	return params, true
}

// CloneStringSlice returns a distinct copy of in, preserving nil vs empty.
func CloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// WorkspacePath returns the /workspace-relative path for a local file.
func WorkspacePath(workspace, imagePath string) string {
	workspace = strings.TrimSpace(workspace)
	imagePath = strings.TrimSpace(imagePath)
	if workspace == "" || imagePath == "" {
		return ""
	}
	rel, err := filepath.Rel(workspace, imagePath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	return filepath.ToSlash(filepath.Join("/workspace", rel))
}
