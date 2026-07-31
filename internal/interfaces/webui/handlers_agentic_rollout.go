package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/agenticrollout"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/VictoriaMetrics/metrics"
	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

const (
	agenticRolloutReadLimit  = 100
	agenticRolloutWriteLimit = 200
)

type putAgenticRolloutRequest struct {
	ExpectedRevision string                   `json:"expected_revision"`
	Changes          agenticrollout.ChangeSet `json:"changes"`
}

type agenticBatchResultView struct {
	DryRun bool                   `json:"dry_run"`
	Items  []agenticBatchItemView `json:"items"`
	Bot    AgenticBotView         `json:"bot"`
}

type agenticBatchItemView struct {
	ChatID string             `json:"chat_id"`
	Before AgenticRolloutView `json:"before"`
	After  AgenticRolloutView `json:"after"`
}

type rolloutErrorResponse struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

func (s *Server) handleGetAgenticRollout(
	w http.ResponseWriter,
	r *http.Request,
) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" || chatID == globalChatToken {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"chat id is required",
		)
		return
	}
	if s.rollouts == nil {
		writeRolloutError(
			w,
			http.StatusServiceUnavailable,
			"rollout_unavailable",
			"agentic rollout service unavailable",
		)
		return
	}
	state, err := s.rollouts.ResolveChat(r.Context(), chatID)
	if err != nil {
		s.writeAgenticRolloutDomainError(w, r, err, nil, 1, "read")
		return
	}
	metrics.GetOrCreateCounter(
		`betago_webui_agentic_rollout_reads_total{scope="single"}`,
	).Inc()
	writeJSON(w, http.StatusOK, s.rolloutView(state))
}

func (s *Server) handleListAgenticRollouts(
	w http.ResponseWriter,
	r *http.Request,
) {
	if s.rollouts == nil {
		writeRolloutError(
			w,
			http.StatusServiceUnavailable,
			"rollout_unavailable",
			"agentic rollout service unavailable",
		)
		return
	}
	chatIDs, err := parseAgenticChatIDs(r.URL.Query().Get("chat_ids"))
	if err != nil {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			err.Error(),
		)
		return
	}
	if len(chatIDs) > agenticRolloutReadLimit {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"at most 100 chat ids may be read at once",
		)
		return
	}
	states, err := s.rollouts.ResolveChats(r.Context(), chatIDs)
	if err != nil {
		s.writeAgenticRolloutDomainError(
			w, r, err, nil, len(chatIDs), "read",
		)
		return
	}
	items := make([]AgenticRolloutView, 0, len(states))
	for _, state := range states {
		items = append(items, s.rolloutView(state))
	}
	metrics.GetOrCreateCounter(
		`betago_webui_agentic_rollout_reads_total{scope="batch"}`,
	).Inc()
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (s *Server) handlePutAgenticRollout(
	w http.ResponseWriter,
	r *http.Request,
) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" || chatID == globalChatToken {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"chat id is required",
		)
		return
	}
	if s.rollouts == nil {
		writeRolloutError(
			w,
			http.StatusServiceUnavailable,
			"rollout_unavailable",
			"agentic rollout service unavailable",
		)
		return
	}
	var request putAgenticRolloutRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"invalid request body: "+err.Error(),
		)
		return
	}
	batchRequest := agenticrollout.BatchRequest{
		ChatIDs:           []string{chatID},
		ExpectedRevisions: map[string]string{chatID: request.ExpectedRevision},
		Changes:           request.Changes,
	}
	s.applyAgenticRollout(w, r, batchRequest)
}

func (s *Server) handleBatchAgenticRollouts(
	w http.ResponseWriter,
	r *http.Request,
) {
	if s.rollouts == nil {
		writeRolloutError(
			w,
			http.StatusServiceUnavailable,
			"rollout_unavailable",
			"agentic rollout service unavailable",
		)
		return
	}
	var request agenticrollout.BatchRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"invalid request body: "+err.Error(),
		)
		return
	}
	if len(request.ChatIDs) > agenticRolloutWriteLimit {
		writeRolloutError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			"at most 200 chat ids may be changed at once",
		)
		return
	}
	s.applyAgenticRollout(w, r, request)
}

func (s *Server) applyAgenticRollout(
	w http.ResponseWriter,
	r *http.Request,
	request agenticrollout.BatchRequest,
) {
	result, err := s.rollouts.Apply(r.Context(), request)
	if err != nil {
		metrics.GetOrCreateCounter(
			`betago_webui_agentic_rollout_mutations_total{dry_run="` +
				boolMetricLabel(request.DryRun) + `",status="error"}`,
		).Inc()
		s.writeAgenticRolloutDomainError(
			w,
			r,
			err,
			request.Changes,
			len(request.ChatIDs),
			operationLabel(request.DryRun),
		)
		return
	}
	metrics.GetOrCreateCounter(
		`betago_webui_agentic_rollout_mutations_total{dry_run="` +
			boolMetricLabel(request.DryRun) + `",status="ok"}`,
	).Inc()
	metrics.GetOrCreateCounter(
		`betago_webui_agentic_rollout_chats_total{operation="` +
			operationLabel(request.DryRun) + `"}`,
	).Add(len(request.ChatIDs))
	s.logAgenticRollout(
		r,
		"agentic rollout applied",
		"ok",
		request.Changes,
		len(request.ChatIDs),
		operationLabel(request.DryRun),
	)
	writeJSON(w, http.StatusOK, s.batchResultView(result))
}

func (s *Server) writeAgenticRolloutDomainError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	changes agenticrollout.ChangeSet,
	chatCount int,
	operation string,
) {
	status, code, message := mapAgenticRolloutError(err)
	if errors.Is(err, agenticrollout.ErrStaleRevision) {
		metrics.GetOrCreateCounter(
			`betago_webui_agentic_rollout_conflicts_total`,
		).Inc()
	}
	if errors.Is(err, agenticrollout.ErrUnavailable) {
		capability := firstChangedCapability(changes)
		metrics.GetOrCreateCounter(
			`betago_webui_agentic_rollout_unavailable_total{capability="` +
				string(capability) + `"}`,
		).Inc()
	}
	s.logAgenticRollout(
		r,
		"agentic rollout request failed",
		code,
		changes,
		chatCount,
		operation,
	)
	writeRolloutError(w, status, code, message)
}

func mapAgenticRolloutError(err error) (int, string, string) {
	switch {
	case errors.Is(err, agenticrollout.ErrInvalidRequest):
		return http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, agenticrollout.ErrStaleRevision):
		return http.StatusConflict, "stale_revision",
			"rollout state changed; reload and retry"
	case errors.Is(err, agenticrollout.ErrUnavailable):
		return http.StatusUnprocessableEntity, "capability_unavailable",
			err.Error()
	case errors.Is(err, agenticrollout.ErrPersistence):
		return http.StatusServiceUnavailable, "persistence_unavailable",
			"rollout persistence unavailable"
	default:
		return http.StatusInternalServerError, "internal_error",
			"unexpected rollout error"
	}
}

func writeRolloutError(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
) {
	writeJSON(w, status, rolloutErrorResponse{Code: code, Error: message})
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := sonic.ConfigDefault.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func parseAgenticChatIDs(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	chatIDs := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		chatID := strings.TrimSpace(part)
		if chatID == "" || chatID == globalChatToken {
			return nil, errors.New("chat_ids must contain non-empty chat ids")
		}
		if _, exists := seen[chatID]; exists {
			return nil, errors.New("chat_ids must not contain duplicates")
		}
		seen[chatID] = struct{}{}
		chatIDs = append(chatIDs, chatID)
	}
	return chatIDs, nil
}

func (s *Server) rolloutView(
	state agenticrollout.ChatState,
) AgenticRolloutView {
	return AgenticRolloutView{
		ChatState: state,
		Bot:       s.agenticBotView(),
	}
}

func (s *Server) batchResultView(
	result agenticrollout.BatchResult,
) agenticBatchResultView {
	items := make([]agenticBatchItemView, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, agenticBatchItemView{
			ChatID: item.ChatID,
			Before: s.rolloutView(item.Before),
			After:  s.rolloutView(item.After),
		})
	}
	return agenticBatchResultView{
		DryRun: result.DryRun,
		Items:  items,
		Bot:    s.agenticBotView(),
	}
}

func (s *Server) agenticBotView() AgenticBotView {
	name := s.robotName
	if name == "" {
		name = "unknown"
	}
	return AgenticBotView{ID: s.botID, Name: name}
}

func (s *Server) logAgenticRollout(
	r *http.Request,
	message string,
	code string,
	changes agenticrollout.ChangeSet,
	chatCount int,
	operation string,
) {
	fields := []zap.Field{
		zap.String("request_id", strings.TrimSpace(r.Header.Get("X-Request-ID"))),
		zap.String("bot_namespace_hash", hashAgenticNamespace(s.botID)),
		zap.Int("chat_count", chatCount),
		zap.Strings("capabilities", sortedCapabilityStrings(changes)),
		zap.String("operation", operation),
		zap.String("code", code),
	}
	if code == "ok" {
		logs.L().Ctx(r.Context()).Info(message, fields...)
		return
	}
	logs.L().Ctx(r.Context()).Warn(message, fields...)
}

func hashAgenticNamespace(namespace string) string {
	sum := sha256.Sum256([]byte(namespace))
	return hex.EncodeToString(sum[:8])
}

func sortedCapabilityStrings(
	changes agenticrollout.ChangeSet,
) []string {
	keys := make([]string, 0, len(changes))
	for capability := range changes {
		keys = append(keys, string(capability))
	}
	sort.Strings(keys)
	return keys
}

func firstChangedCapability(
	changes agenticrollout.ChangeSet,
) agenticrollout.Capability {
	keys := sortedCapabilityStrings(changes)
	if len(keys) == 0 {
		return "unknown"
	}
	return agenticrollout.Capability(keys[0])
}

func boolMetricLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func operationLabel(dryRun bool) string {
	if dryRun {
		return "preview"
	}
	return "commit"
}
