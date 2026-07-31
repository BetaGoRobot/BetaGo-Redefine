package agenticrollout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
)

type ServiceOptions struct {
	Store     ConfigStore
	Static    StaticPolicies
	Namespace string
}

type Service struct {
	store     ConfigStore
	static    StaticPolicies
	namespace string
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: config store is required", ErrInvalidRequest)
	}
	if options.Static.EvaluationAllows == nil {
		options.Static.EvaluationAllows = func(string) bool { return false }
	}
	if options.Static.AgentCardAllows == nil {
		options.Static.AgentCardAllows = func(string) bool { return false }
	}
	return &Service{
		store:     options.Store,
		static:    options.Static,
		namespace: strings.TrimSpace(options.Namespace),
	}, nil
}

func (s *Service) ResolveChat(
	ctx context.Context,
	chatID string,
) (ChatState, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ChatState{}, fmt.Errorf("%w: chat id is required", ErrInvalidRequest)
	}
	state := ChatState{
		ChatID:       chatID,
		Capabilities: make([]CapabilityState, 0, len(orderedCapabilities)),
	}
	for _, capability := range orderedCapabilities {
		state.Capabilities = append(
			state.Capabilities,
			s.resolveCapability(ctx, chatID, capability),
		)
	}
	state.Revision = s.revision(state)
	return state, nil
}

func (s *Service) ResolveChats(
	ctx context.Context,
	chatIDs []string,
) ([]ChatState, error) {
	normalized, err := normalizeChatIDs(chatIDs)
	if err != nil {
		return nil, err
	}
	states := make([]ChatState, 0, len(normalized))
	for _, chatID := range normalized {
		state, resolveErr := s.ResolveChat(ctx, chatID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		states = append(states, state)
	}
	return states, nil
}

func (s *Service) resolveCapability(
	ctx context.Context,
	chatID string,
	capability Capability,
) CapabilityState {
	key := configKey(capability)
	baseline, baselineSource := s.baseline(ctx, chatID, capability, key)
	available, reason := s.availability(capability)
	state := CapabilityState{
		Key:       capability,
		Label:     capabilityLabel(capability),
		Override:  OverrideInherit,
		Baseline:  baseline,
		Effective: baseline && available,
		Source:    baselineSource,
		Available: available,
		Reason:    reason,
	}
	if value, configured := s.store.GetScopedBoolOverride(
		ctx,
		key,
		appconfig.ScopeChat,
		chatID,
		"",
	); configured {
		state.Override = overrideFromBool(value)
		state.Effective = value && available
		state.Source = SourceChatOverride
	}
	return state
}

func (s *Service) baseline(
	ctx context.Context,
	chatID string,
	capability Capability,
	key appconfig.ConfigKey,
) (bool, Source) {
	if value, configured := s.store.GetScopedBoolOverride(
		ctx,
		key,
		appconfig.ScopeGlobal,
		"",
		"",
	); configured {
		return value, SourceGlobalConfig
	}
	switch capability {
	case ParallelEvaluation:
		return s.static.EvaluationAllows(chatID), SourceTOML
	case AgentCard:
		return s.static.AgentCardAllows(chatID), SourceTOML
	default:
		return false, SourceDefault
	}
}

func (s *Service) availability(capability Capability) (bool, string) {
	switch capability {
	case ParallelEvaluation:
		if !s.static.EvaluationAvailable {
			return false, "parallel_evaluation_not_initialized"
		}
	case AgentCard:
		if !s.static.AgentCardAvailable {
			reason := strings.TrimSpace(s.static.AgentCardUnavailableReason)
			if reason == "" {
				reason = "agent_card_not_initialized"
			}
			return false, reason
		}
	}
	return true, ""
}

func (s *Service) Apply(
	ctx context.Context,
	request BatchRequest,
) (BatchResult, error) {
	chatIDs, err := normalizeChatIDs(request.ChatIDs)
	if err != nil {
		return BatchResult{}, err
	}
	if err := validateChangeSet(request.Changes); err != nil {
		return BatchResult{}, err
	}
	before, err := s.ResolveChats(ctx, chatIDs)
	if err != nil {
		return BatchResult{}, err
	}
	if err := validateExpectedRevisions(before, request.ExpectedRevisions); err != nil {
		return BatchResult{}, err
	}
	preview, err := s.projectBatch(ctx, before, request.Changes)
	if err != nil {
		return BatchResult{}, err
	}
	preview.DryRun = request.DryRun
	if request.DryRun {
		return preview, nil
	}

	mutations := make([]appconfig.ConfigMutation, 0, len(chatIDs)*len(request.Changes))
	capabilities := sortedChangedCapabilities(request.Changes)
	for _, chatID := range chatIDs {
		for _, capability := range capabilities {
			override := request.Changes[capability]
			mutation := appconfig.ConfigMutation{
				Key:    configKey(capability),
				Scope:  appconfig.ScopeChat,
				ChatID: chatID,
			}
			if override != OverrideInherit {
				value := strconv.FormatBool(override == OverrideEnabled)
				mutation.Value = &value
			}
			mutations = append(mutations, mutation)
		}
	}
	guard := func() error {
		current, resolveErr := s.ResolveChats(ctx, chatIDs)
		if resolveErr != nil {
			return resolveErr
		}
		if revisionErr := validateExpectedRevisions(
			current,
			request.ExpectedRevisions,
		); revisionErr != nil {
			return revisionErr
		}
		_, projectErr := s.projectBatch(ctx, current, request.Changes)
		return projectErr
	}
	if err := s.store.ApplyConfigMutations(ctx, mutations, guard); err != nil {
		if errors.Is(err, ErrStaleRevision) ||
			errors.Is(err, ErrUnavailable) ||
			errors.Is(err, ErrInvalidRequest) {
			return BatchResult{}, err
		}
		return BatchResult{}, fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	after, err := s.ResolveChats(ctx, chatIDs)
	if err != nil {
		return BatchResult{}, err
	}
	result := BatchResult{Items: make([]BatchItem, 0, len(chatIDs))}
	for index, chatID := range chatIDs {
		result.Items = append(result.Items, BatchItem{
			ChatID: chatID,
			Before: before[index],
			After:  after[index],
		})
	}
	return result, nil
}

func (s *Service) projectBatch(
	ctx context.Context,
	before []ChatState,
	changes ChangeSet,
) (BatchResult, error) {
	result := BatchResult{Items: make([]BatchItem, 0, len(before))}
	for _, state := range before {
		after := ChatState{
			ChatID:       state.ChatID,
			Capabilities: append([]CapabilityState(nil), state.Capabilities...),
		}
		for capability, override := range changes {
			index := capabilityIndex(after.Capabilities, capability)
			if index < 0 {
				return BatchResult{}, fmt.Errorf(
					"%w: unknown capability %q",
					ErrInvalidRequest,
					capability,
				)
			}
			current := after.Capabilities[index]
			if override == OverrideEnabled && !current.Available {
				return BatchResult{}, fmt.Errorf(
					"%w: chat %s capability %s: %s",
					ErrUnavailable,
					state.ChatID,
					capability,
					current.Reason,
				)
			}
			current.Override = override
			switch override {
			case OverrideInherit:
				baseline, source := s.baseline(
					ctx,
					state.ChatID,
					capability,
					configKey(capability),
				)
				current.Baseline = baseline
				current.Effective = current.Baseline && current.Available
				current.Source = source
			case OverrideEnabled:
				current.Effective = current.Available
				current.Source = SourceChatOverride
			case OverrideDisabled:
				current.Effective = false
				current.Source = SourceChatOverride
			}
			after.Capabilities[index] = current
		}
		after.Revision = s.revision(after)
		result.Items = append(result.Items, BatchItem{
			ChatID: state.ChatID,
			Before: state,
			After:  after,
		})
	}
	return result, nil
}

func (s *Service) RuntimeEnabled(ctx context.Context, chatID string) bool {
	return s.enabled(ctx, chatID, ConversationRuntime)
}

func (s *Service) CallbackContinuationEnabled(
	ctx context.Context,
	chatID string,
) bool {
	return s.enabled(ctx, chatID, CallbackContinuation)
}

func (s *Service) EvaluationEnabled(ctx context.Context, chatID string) bool {
	return s.enabled(ctx, chatID, ParallelEvaluation)
}

func (s *Service) AgentCardEnabled(ctx context.Context, chatID string) bool {
	return s.enabled(ctx, chatID, AgentCard)
}

func (s *Service) enabled(
	ctx context.Context,
	chatID string,
	capability Capability,
) bool {
	state, err := s.ResolveChat(ctx, chatID)
	if err != nil {
		return false
	}
	return state.Capability(capability).Effective
}

func (s *Service) revision(state ChatState) string {
	type revisionCapability struct {
		Key       Capability    `json:"key"`
		Override  OverrideState `json:"override"`
		Baseline  bool          `json:"baseline"`
		Effective bool          `json:"effective"`
		Source    Source        `json:"source"`
		Available bool          `json:"available"`
		Reason    string        `json:"reason,omitempty"`
	}
	payload := struct {
		Namespace    string               `json:"namespace"`
		ChatID       string               `json:"chat_id"`
		Capabilities []revisionCapability `json:"capabilities"`
	}{
		Namespace: s.namespace,
		ChatID:    state.ChatID,
	}
	for _, capability := range state.Capabilities {
		payload.Capabilities = append(payload.Capabilities, revisionCapability{
			Key: capability.Key, Override: capability.Override,
			Baseline: capability.Baseline, Effective: capability.Effective,
			Source: capability.Source, Available: capability.Available,
			Reason: capability.Reason,
		})
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeChatIDs(chatIDs []string) ([]string, error) {
	if len(chatIDs) == 0 {
		return nil, fmt.Errorf("%w: at least one chat id is required", ErrInvalidRequest)
	}
	normalized := make([]string, 0, len(chatIDs))
	seen := make(map[string]struct{}, len(chatIDs))
	for _, raw := range chatIDs {
		chatID := strings.TrimSpace(raw)
		if chatID == "" {
			return nil, fmt.Errorf("%w: chat id is required", ErrInvalidRequest)
		}
		if _, exists := seen[chatID]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate chat id %q",
				ErrInvalidRequest,
				chatID,
			)
		}
		seen[chatID] = struct{}{}
		normalized = append(normalized, chatID)
	}
	return normalized, nil
}

func validateChangeSet(changes ChangeSet) error {
	if len(changes) == 0 {
		return fmt.Errorf("%w: changes are required", ErrInvalidRequest)
	}
	for capability, override := range changes {
		if configKey(capability) == "" {
			return fmt.Errorf(
				"%w: unknown capability %q",
				ErrInvalidRequest,
				capability,
			)
		}
		switch override {
		case OverrideInherit, OverrideEnabled, OverrideDisabled:
		default:
			return fmt.Errorf(
				"%w: invalid override %q",
				ErrInvalidRequest,
				override,
			)
		}
	}
	return nil
}

func validateExpectedRevisions(
	states []ChatState,
	expected map[string]string,
) error {
	for _, state := range states {
		if strings.TrimSpace(expected[state.ChatID]) != state.Revision {
			return fmt.Errorf(
				"%w: chat %s",
				ErrStaleRevision,
				state.ChatID,
			)
		}
	}
	return nil
}

func sortedChangedCapabilities(changes ChangeSet) []Capability {
	capabilities := make([]Capability, 0, len(changes))
	for capability := range changes {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(i, j int) bool {
		return capabilityOrder(capabilities[i]) < capabilityOrder(capabilities[j])
	})
	return capabilities
}

func capabilityOrder(capability Capability) int {
	for index, candidate := range orderedCapabilities {
		if candidate == capability {
			return index
		}
	}
	return len(orderedCapabilities)
}

func capabilityIndex(states []CapabilityState, capability Capability) int {
	for index, state := range states {
		if state.Key == capability {
			return index
		}
	}
	return -1
}

func overrideFromBool(value bool) OverrideState {
	if value {
		return OverrideEnabled
	}
	return OverrideDisabled
}

func configKey(capability Capability) appconfig.ConfigKey {
	switch capability {
	case ConversationRuntime:
		return appconfig.KeyConversationRuntimeEnabled
	case CallbackContinuation:
		return appconfig.KeyConversationCallbackContinuationEnabled
	case ParallelEvaluation:
		return appconfig.KeyConversationParallelEvaluationEnabled
	case AgentCard:
		return appconfig.KeyAgentCardEnabled
	default:
		return ""
	}
}

func capabilityLabel(capability Capability) string {
	switch capability {
	case ConversationRuntime:
		return "Conversation Runtime"
	case CallbackContinuation:
		return "Callback Continuation"
	case ParallelEvaluation:
		return "Parallel Evaluation"
	case AgentCard:
		return "Agent Card"
	default:
		return string(capability)
	}
}
