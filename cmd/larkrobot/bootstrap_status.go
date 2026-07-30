package main

import (
	"sync"
	"time"
)

type bootstrapStatus struct {
	mu     sync.RWMutex
	values map[string]any
}

func newBootstrapStatus(initial map[string]any) *bootstrapStatus {
	status := &bootstrapStatus{values: make(map[string]any, len(initial)+3)}
	for key, value := range initial {
		status.values[key] = value
	}
	status.values["succeeded"] = false
	return status
}

func (s *bootstrapStatus) Update(values map[string]any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range values {
		s.values[key] = value
	}
}

func (s *bootstrapStatus) Complete(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values["completed_at"] = time.Now().UTC()
	s.values["succeeded"] = err == nil
	if err == nil {
		delete(s.values, "last_error")
		return
	}
	s.values["last_error"] = err.Error()
}

func (s *bootstrapStatus) Stats() map[string]any {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]any, len(s.values))
	for key, value := range s.values {
		result[key] = value
	}
	return result
}
