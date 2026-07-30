package webui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) requireSensitiveRead(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	if s == nil || s.authToken == "" {
		writeError(
			w,
			http.StatusServiceUnavailable,
			"sensitive reads require webui auth_token",
		)
		return false
	}
	if !s.checkBearer(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *Server) handleListEvaluations(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !s.requireSensitiveRead(w, r) {
		return
	}
	if s.evaluations == nil || s.appID == "" || s.botOpenID == "" {
		writeError(w, http.StatusServiceUnavailable, "evaluation workbench unavailable")
		return
	}
	query, err := s.parseEvaluationListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := s.evaluations.ListEpisodes(r.Context(), query)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) parseEvaluationListQuery(
	r *http.Request,
) (EvaluationListQuery, error) {
	values := r.URL.Query()
	to := s.now().UTC()
	from := to.Add(-24 * time.Hour)
	var err error
	if raw := strings.TrimSpace(values.Get("from")); raw != "" {
		from, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return EvaluationListQuery{}, ErrInvalidEvaluationQuery
		}
	}
	if raw := strings.TrimSpace(values.Get("to")); raw != "" {
		to, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return EvaluationListQuery{}, ErrInvalidEvaluationQuery
		}
	}
	limit := 50
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return EvaluationListQuery{}, ErrInvalidEvaluationQuery
		}
	}
	query := EvaluationListQuery{
		AppID: s.appID, BotOpenID: s.botOpenID,
		ChatID:   strings.TrimSpace(values.Get("chat_id")),
		CohortID: strings.TrimSpace(values.Get("cohort_id")),
		Status:   strings.TrimSpace(values.Get("status")),
		Winner:   strings.TrimSpace(values.Get("winner")),
		From:     from, To: to, Limit: limit,
	}
	if raw := strings.TrimSpace(values.Get("needs_review")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return EvaluationListQuery{}, ErrInvalidEvaluationQuery
		}
		query.NeedsReview = &value
	}
	if raw := strings.TrimSpace(values.Get("cursor")); raw != "" {
		cursor, decodeErr := DecodeEvaluationCursor(raw)
		if decodeErr != nil {
			return EvaluationListQuery{}, decodeErr
		}
		query.CursorAnchorAt = cursor.AnchorAt
		query.CursorID = cursor.EpisodeID
	}
	if err := query.validate(); err != nil {
		return EvaluationListQuery{}, err
	}
	return query, nil
}

func (s *Server) handleGetEvaluation(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !s.requireSensitiveRead(w, r) {
		return
	}
	if s.evaluations == nil || s.appID == "" || s.botOpenID == "" {
		writeError(w, http.StatusServiceUnavailable, "evaluation workbench unavailable")
		return
	}
	episodeID := strings.TrimSpace(r.PathValue("episodeID"))
	if episodeID == "" {
		writeError(w, http.StatusBadRequest, "episode id is required")
		return
	}
	detail, err := s.evaluations.GetEpisode(
		r.Context(),
		s.appID,
		s.botOpenID,
		episodeID,
	)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func writeEvaluationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidEvaluationQuery):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrEvaluationNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrEvaluationUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "evaluation query failed")
	}
}

func (s *Server) handleAppendEvaluationJudgment(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !s.requireSensitiveRead(w, r) {
		return
	}
	if s.evaluations == nil || s.appID == "" || s.botOpenID == "" {
		writeError(w, http.StatusServiceUnavailable, "evaluation workbench unavailable")
		return
	}
	var request HumanJudgmentRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid judgment document")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid judgment document")
		return
	}
	request.ScoresJSON = bytes.TrimSpace(request.ScoresJSON)
	if err := request.validate(); err != nil {
		writeEvaluationError(w, err)
		return
	}
	episodeID := strings.TrimSpace(r.PathValue("episodeID"))
	judgment, err := s.evaluations.AppendHumanJudgment(
		r.Context(),
		s.appID,
		s.botOpenID,
		episodeID,
		request,
	)
	if err != nil {
		writeEvaluationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, judgment)
}
