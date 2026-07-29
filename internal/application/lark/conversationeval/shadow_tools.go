package conversationeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/toolmeta"
)

type ToolObservation struct {
	EpisodeID           string          `json:"episode_id"`
	ToolName            string          `json:"tool_name"`
	CanonicalArguments  json.RawMessage `json:"canonical_arguments"`
	Output              string          `json:"output"`
	SourceLane          Lane            `json:"source_lane"`
	ReplayedFromControl bool            `json:"replayed_from_control"`
	ObservedAt          time.Time       `json:"observed_at"`
}

var candidateShadowToolNames = []string{
	"search_history",
	"finance_tool_discover",
	"finance_market_data_get",
	"finance_news_get",
	"economy_indicator_get",
	"get_chat_members",
	"get_recent_active_members",
}

var candidateShadowToolSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(candidateShadowToolNames))
	for _, name := range candidateShadowToolNames {
		result[name] = struct{}{}
	}
	return result
}()

var candidateReplayOnlyToolSet = map[string]struct{}{
	"get_recent_active_members": {},
}

func CandidateShadowToolNames() []string {
	return append([]string(nil), candidateShadowToolNames...)
}

type observationKey struct {
	episodeID string
	toolName  string
	arguments string
}

type ShadowToolFunc func(context.Context, json.RawMessage) (string, error)

type shadowInvocationRecorderKey struct{}

type shadowInvocationRecorder struct {
	mu     sync.Mutex
	values []ToolObservation
}

func withShadowInvocationRecorder(ctx context.Context) (context.Context, *shadowInvocationRecorder) {
	recorder := &shadowInvocationRecorder{}
	return context.WithValue(ctx, shadowInvocationRecorderKey{}, recorder), recorder
}

func recordShadowInvocation(ctx context.Context, observation ToolObservation) {
	if ctx == nil {
		return
	}
	recorder, _ := ctx.Value(shadowInvocationRecorderKey{}).(*shadowInvocationRecorder)
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.values = append(recorder.values, cloneCaptureValue(observation))
	recorder.mu.Unlock()
}

func (r *shadowInvocationRecorder) Snapshot() []ToolObservation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneCaptureValue(r.values)
}

type ShadowToolRegistry struct {
	mu       sync.RWMutex
	cache    *ObservationCache
	tools    map[string]ShadowToolFunc
	anchorAt time.Time
	now      func() time.Time
}

func NewShadowToolRegistry(cache *ObservationCache) *ShadowToolRegistry {
	if cache == nil {
		cache = NewObservationCache()
	}
	return &ShadowToolRegistry{
		cache: cache,
		tools: make(map[string]ShadowToolFunc),
		now:   time.Now,
	}
}

func NewAnchoredShadowToolRegistry(
	cache *ObservationCache,
	anchorAt time.Time,
) *ShadowToolRegistry {
	registry := NewShadowToolRegistry(cache)
	registry.anchorAt = anchorAt
	return registry
}

func (r *ShadowToolRegistry) validateAnchor(anchorAt time.Time) error {
	if r == nil {
		return fmt.Errorf("candidate shadow tool registry is nil")
	}
	if r.anchorAt.IsZero() {
		return fmt.Errorf("candidate shadow tool registry anchor is required")
	}
	if anchorAt.IsZero() || !r.anchorAt.Equal(anchorAt) {
		return fmt.Errorf("candidate shadow tool registry anchor does not match request anchor")
	}
	return nil
}

func (r *ShadowToolRegistry) Register(name string, tool ShadowToolFunc) error {
	if r == nil {
		return fmt.Errorf("shadow tool registry is nil")
	}
	name, err := validateReadOnlyObservationTool(name)
	if err != nil {
		return err
	}
	if tool == nil {
		return fmt.Errorf("shadow tool %q handler is nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
	return nil
}

func (r *ShadowToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

func (r *ShadowToolRegistry) Invoke(
	ctx context.Context,
	episodeID, name string,
	arguments json.RawMessage,
) (ToolObservation, error) {
	if r == nil {
		return ToolObservation{}, fmt.Errorf("shadow tool registry is nil")
	}
	name, err := validateReadOnlyObservationTool(name)
	if err != nil {
		return ToolObservation{}, err
	}
	r.mu.RLock()
	tool, registered := r.tools[name]
	r.mu.RUnlock()
	if !registered {
		return ToolObservation{}, fmt.Errorf("shadow tool %q is not registered", name)
	}
	if replayed, ok, replayErr := r.cache.Replay(ctx, episodeID, name, arguments); replayErr != nil {
		return ToolObservation{}, replayErr
	} else if ok {
		recordShadowInvocation(ctx, replayed)
		return replayed, nil
	}
	if _, replayOnly := candidateReplayOnlyToolSet[name]; replayOnly {
		return ToolObservation{}, fmt.Errorf(
			"shadow tool %q is replay-only and no exact control observation exists",
			name,
		)
	}
	canonical, err := canonicalToolArguments(arguments)
	if err != nil {
		return ToolObservation{}, err
	}
	if name == "search_history" {
		canonical, err = ClampCandidateSearchHistoryArguments(canonical, r.anchorAt)
		if err != nil {
			return ToolObservation{}, err
		}
	}
	output, err := tool(ctx, canonical)
	if err != nil {
		return ToolObservation{}, err
	}
	observation := ToolObservation{
		EpisodeID:          strings.TrimSpace(episodeID),
		ToolName:           name,
		CanonicalArguments: append(json.RawMessage(nil), canonical...),
		Output:             output,
		SourceLane:         LaneCandidate,
	}
	if r.now != nil {
		observation.ObservedAt = r.now()
	} else {
		observation.ObservedAt = time.Now()
	}
	recordShadowInvocation(ctx, observation)
	return observation, nil
}

func ClampCandidateSearchHistoryArguments(
	arguments json.RawMessage,
	anchorAt time.Time,
) (json.RawMessage, error) {
	if anchorAt.IsZero() {
		return nil, fmt.Errorf("candidate search_history anchor is required")
	}
	object := make(map[string]any)
	if len(bytes.TrimSpace(arguments)) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return nil, fmt.Errorf("decode candidate search_history arguments: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("decode candidate search_history arguments: multiple JSON values")
			}
			return nil, fmt.Errorf("decode candidate search_history arguments: %w", err)
		}
		if object == nil {
			return nil, fmt.Errorf("candidate search_history arguments must be a JSON object")
		}
	}

	endTime, _ := object["end_time"].(string)
	if parsed, ok := parseCandidateSearchHistoryEndTime(endTime); !ok || parsed.After(anchorAt) {
		object["end_time"] = anchorAt.Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode candidate search_history arguments: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func parseCandidateSearchHistoryEndTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.DateTime} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type ObservationCache struct {
	mu     sync.RWMutex
	values map[observationKey]ToolObservation
	now    func() time.Time
}

func NewObservationCache() *ObservationCache {
	return &ObservationCache{
		values: make(map[observationKey]ToolObservation),
		now:    time.Now,
	}
}

func (c *ObservationCache) RecordControl(
	_ context.Context,
	episodeID string,
	trace ToolTrace,
) error {
	if c == nil {
		return fmt.Errorf("observation cache is nil")
	}
	toolName, err := validateReadOnlyObservationTool(trace.Name)
	if err != nil {
		return err
	}
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return fmt.Errorf("observation episode id is required")
	}
	if trace.Pending {
		return fmt.Errorf("control observation %q is still pending", toolName)
	}
	arguments, err := canonicalToolArguments(trace.Arguments)
	if err != nil {
		return err
	}
	key := observationKey{episodeID: episodeID, toolName: toolName, arguments: string(arguments)}
	value := ToolObservation{
		EpisodeID:          episodeID,
		ToolName:           toolName,
		CanonicalArguments: append(json.RawMessage(nil), arguments...),
		Output:             trace.Output,
		SourceLane:         LaneControl,
	}
	if c.now != nil {
		value.ObservedAt = c.now()
	} else {
		value.ObservedAt = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
	return nil
}

func (c *ObservationCache) RecordControlSnapshot(
	ctx context.Context,
	episodeID string,
	snapshot CaptureSnapshot,
) error {
	if snapshot.Output == nil {
		return nil
	}
	return c.RecordControlTraces(ctx, episodeID, snapshot.Output.CapabilityCalls)
}

func (c *ObservationCache) RecordControlTraces(
	ctx context.Context,
	episodeID string,
	traces []ToolTrace,
) error {
	for _, trace := range traces {
		name := strings.TrimSpace(trace.Name)
		if trace.Pending {
			continue
		}
		if _, allowed := candidateShadowToolSet[name]; !allowed {
			continue
		}
		if err := c.RecordControl(ctx, episodeID, trace); err != nil {
			return err
		}
	}
	return nil
}

func (c *ObservationCache) Replay(
	_ context.Context,
	episodeID, toolName string,
	arguments json.RawMessage,
) (ToolObservation, bool, error) {
	if c == nil {
		return ToolObservation{}, false, fmt.Errorf("observation cache is nil")
	}
	toolName, err := validateReadOnlyObservationTool(toolName)
	if err != nil {
		return ToolObservation{}, false, err
	}
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return ToolObservation{}, false, fmt.Errorf("observation episode id is required")
	}
	canonical, err := canonicalToolArguments(arguments)
	if err != nil {
		return ToolObservation{}, false, err
	}
	key := observationKey{episodeID: episodeID, toolName: toolName, arguments: string(canonical)}
	c.mu.RLock()
	value, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		return ToolObservation{}, false, nil
	}
	value.CanonicalArguments = append(json.RawMessage(nil), value.CanonicalArguments...)
	value.ReplayedFromControl = true
	return value, true, nil
}

func validateReadOnlyObservationTool(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("observation tool name is required")
	}
	behavior, explicit := toolmeta.LookupRuntimeBehavior(name)
	if !explicit {
		return "", fmt.Errorf("observation tool %q has no explicit runtime behavior", name)
	}
	if behavior.SideEffectLevel != toolmeta.SideEffectLevelNone {
		return "", fmt.Errorf(
			"observation tool %q has side effect level %q",
			name,
			behavior.SideEffectLevel,
		)
	}
	if _, allowed := candidateShadowToolSet[name]; !allowed {
		return "", fmt.Errorf("observation tool %q is outside the candidate allowlist", name)
	}
	return name, nil
}

func canonicalToolArguments(arguments json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("canonicalize tool arguments: %w", err)
	}
	if value == nil {
		return nil, fmt.Errorf("canonicalize tool arguments: expected JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("canonicalize tool arguments: multiple JSON values")
		}
		return nil, fmt.Errorf("canonicalize tool arguments: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical tool arguments: %w", err)
	}
	return json.RawMessage(canonical), nil
}
