package capability

import (
	"fmt"
	"sort"
	"sync"
)

// Registry carries capability runtime state.
type Registry struct {
	mu           sync.RWMutex
	capabilities map[string]Capability
}

// NewRegistry implements capability runtime behavior.
func NewRegistry() *Registry {
	return &Registry{capabilities: make(map[string]Capability)}
}

// Register implements capability runtime behavior.
func (r *Registry) Register(capability Capability) error {
	if capability == nil {
		return fmt.Errorf("capability is nil")
	}
	meta := capability.Meta()
	if meta.Name == "" {
		return fmt.Errorf("capability name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.capabilities[meta.Name]; exists {
		return fmt.Errorf("capability already registered: %s", meta.Name)
	}
	r.capabilities[meta.Name] = capability
	return nil
}

// Get implements capability runtime behavior.
func (r *Registry) Get(name string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	capability, ok := r.capabilities[name]
	return capability, ok
}

// Lookup implements capability runtime behavior.
func (r *Registry) Lookup(name string, scope Scope) (Capability, error) {
	capability, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("capability not found: %s", name)
	}
	if !capability.Meta().AllowsScope(scope) {
		return nil, fmt.Errorf("capability %s does not allow scope %s", name, scope)
	}
	return capability, nil
}

// List implements capability runtime behavior.
func (r *Registry) List() []Meta {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metas := make([]Meta, 0, len(r.capabilities))
	for _, capability := range r.capabilities {
		metas = append(metas, capability.Meta())
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
	return metas
}
