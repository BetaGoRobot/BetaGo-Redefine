package cardregression

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	scenes map[string]CardSceneProtocol
}

var defaultRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{scenes: make(map[string]CardSceneProtocol)}
}

func DefaultRegistry() *Registry {
	return defaultRegistry
}

func Register(scene CardSceneProtocol) error {
	return defaultRegistry.Register(scene)
}

func MustRegister(scene CardSceneProtocol) {
	defaultRegistry.MustRegister(scene)
}

func (r *Registry) Register(scene CardSceneProtocol) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if err := validateScene(scene); err != nil {
		return err
	}
	key := strings.TrimSpace(scene.SceneKey())

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.scenes[key]; exists {
		return fmt.Errorf("duplicate scene key %q", key)
	}
	r.scenes[key] = scene
	return nil
}

func (r *Registry) MustRegister(scene CardSceneProtocol) {
	if err := r.Register(scene); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(sceneKey string) (CardSceneProtocol, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	scene, ok := r.scenes[strings.TrimSpace(sceneKey)]
	return scene, ok
}

func (r *Registry) List() []CardSceneProtocol {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.scenes))
	for key := range r.scenes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]CardSceneProtocol, 0, len(keys))
	for _, key := range keys {
		result = append(result, r.scenes[key])
	}
	return result
}

func (r *Registry) ValidateRegisteredScenes() error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var issues []string
	for key, scene := range r.scenes {
		if strings.TrimSpace(key) == "" {
			issues = append(issues, "blank scene key")
		}
		if scene == nil {
			issues = append(issues, fmt.Sprintf("nil scene for key %q", key))
			continue
		}
		if err := validateScene(scene); err != nil {
			issues = append(issues, err.Error())
		}
	}
	if len(issues) > 0 {
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func validateScene(scene CardSceneProtocol) error {
	if scene == nil {
		return fmt.Errorf("nil scene")
	}
	key := strings.TrimSpace(scene.SceneKey())
	if key == "" {
		return fmt.Errorf("blank scene key")
	}
	cases := scene.TestCases()
	if len(cases) == 0 {
		return fmt.Errorf("scene %q must expose at least one regression case", key)
	}
	seen := make(map[string]struct{}, len(cases))
	for _, c := range cases {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return fmt.Errorf("scene %q contains blank case name", key)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("scene %q contains duplicate case name %q", key, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
