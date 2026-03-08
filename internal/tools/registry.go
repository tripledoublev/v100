package tools

import (
	"fmt"
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

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
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
	specs := make([]providers.ToolSpec, 0, len(r.enabled))
	for name := range r.enabled {
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
	for name := range r.enabled {
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
	out := make([]Tool, 0, len(r.enabled))
	for name := range r.enabled {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// Validate ensures all enabled tool names are actually registered.
func (r *Registry) Validate() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name := range r.enabled {
		if _, ok := r.tools[name]; !ok {
			return fmt.Errorf("tool %q is enabled but not registered", name)
		}
	}
	return nil
}
