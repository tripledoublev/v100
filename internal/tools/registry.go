package tools

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tripledoublev/v100/internal/providers"
)

// Registry holds registered tools and enforces the enabled allowlist.
type Registry struct {
	mu      sync.RWMutex
	tools   map[string]Tool
	enabled map[string]bool
}

// NewRegistry creates a registry with the given tool allowlist.
func NewRegistry(enabledNames []string) *Registry {
	enabled := make(map[string]bool, len(enabledNames))
	for _, n := range enabledNames {
		enabled[n] = true
	}
	return &Registry{
		tools:   make(map[string]Tool),
		enabled: enabled,
	}
}

// Register adds or replaces a tool in the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Enable marks a tool name as allowed for access once it is registered.
func (r *Registry) Enable(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled[name] = true
}

// Disable removes a tool name from the allowlist.
func (r *Registry) Disable(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.enabled, name)
}

// RegisterAndEnable adds or replaces a tool in the registry and marks it enabled.
func (r *Registry) RegisterAndEnable(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	r.enabled[t.Name()] = true
}

// Unregister removes a tool instance from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// IsEnabled reports whether the named tool is currently allowlisted.
func (r *Registry) IsEnabled(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled[name]
}

// Get returns an enabled tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok || !r.enabled[name] {
		return nil, false
	}
	return t, true
}

// Lookup returns a registered tool by name, regardless of enabled state.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// IsDangerous returns true if the named tool is classified as Dangerous.
func (r *Registry) IsDangerous(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return false
	}
	return t.DangerLevel() == Dangerous
}

// Effects returns the execution effects metadata for a registered tool.
func (r *Registry) Effects(name string) ToolEffects {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return ToolEffects{}
	}
	return t.Effects()
}

// Specs returns ToolSpec slices for all enabled tools (used to send to provider).
func (r *Registry) Specs() []providers.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := r.enabledNamesLocked()
	specs := make([]providers.ToolSpec, 0, len(names))
	for _, name := range names {
		t, ok := r.tools[name]
		if !ok {
			continue
		}
		specs = append(specs, providers.ToolSpec{
			Name:         t.Name(),
			Description:  t.Description(),
			InputSchema:  t.InputSchema(),
			OutputSchema: t.OutputSchema(),
		})
	}
	return specs
}

// List returns all enabled tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.enabled))
	for _, name := range r.enabledNamesLocked() {
		if _, ok := r.tools[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

// EnabledTools returns all enabled Tool objects.
func (r *Registry) EnabledTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := r.enabledNamesLocked()
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// RegisteredTools returns all currently registered Tool objects, regardless of enabled state.
func (r *Registry) RegisteredTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

// Validate ensures all enabled tool names are actually registered.
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	missing := r.missingEnabledNamesLocked()
	if len(missing) > 0 {
		if len(missing) == 1 {
			return fmt.Errorf("tool %q is enabled but not registered", missing[0])
		}
		return fmt.Errorf("enabled tools not registered: %s", strings.Join(missing, ", "))
	}
	return nil
}

// MissingEnabledNames returns enabled tool names that are not registered.
func (r *Registry) MissingEnabledNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.missingEnabledNamesLocked()
}

func (r *Registry) enabledNamesLocked() []string {
	names := make([]string, 0, len(r.enabled))
	for name := range r.enabled {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) missingEnabledNamesLocked() []string {
	names := make([]string, 0)
	for _, name := range r.enabledNamesLocked() {
		if _, ok := r.tools[name]; !ok {
			names = append(names, name)
		}
	}
	return names
}
