package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrSurfacePatchPending = errors.New("agent card patch is pending reconciliation")

type LifecycleStore interface {
	TransitionSurface(context.Context, TransitionSurfaceRequest) (*CardSurface, error)
}

type PatchStore interface {
	ClaimPatch(context.Context, ClaimPatchRequest) (*CardSurface, error)
	CompletePatch(context.Context, CompletePatchRequest) error
	RetryPatch(context.Context, RetryPatchRequest) error
}

type PatchCatalog interface {
	ListDuePatches(context.Context, time.Time, int) ([]PatchTarget, error)
}

type LifecycleManager struct {
	store    LifecycleStore
	compiler ArtifactCompiler
	worker   *PatchWorker
	now      func() time.Time
}

func NewLifecycleManager(
	store LifecycleStore,
	compiler ArtifactCompiler,
	worker *PatchWorker,
) *LifecycleManager {
	return &LifecycleManager{
		store: store, compiler: compiler, worker: worker, now: time.Now,
	}
}

type AdvanceSurfaceRequest struct {
	Surface     *CardSurface
	To          SurfaceStatus
	ActionID    string
	ActorOpenID string
	SourceRef   string
	OccurredAt  time.Time
}

func (m *LifecycleManager) AdvanceAndPatch(
	ctx context.Context,
	request AdvanceSurfaceRequest,
) (*CardSurface, error) {
	if m == nil || m.store == nil || m.compiler == nil || m.worker == nil ||
		request.Surface == nil {
		return nil, errors.New("agent card lifecycle manager is not configured")
	}
	state, err := lifecycleStateForStatus(request.To)
	if err != nil {
		return nil, err
	}
	var spec CardSpec
	if err := json.Unmarshal(
		[]byte(request.Surface.SpecJSON),
		&spec,
	); err != nil {
		return nil, ErrCardConflict
	}
	bound, err := NewBoundCardSpec(spec, state, nil)
	if err != nil {
		return nil, ErrCardConflict
	}
	compiled, err := m.compiler.CompileJSON(bound)
	if err != nil || !json.Valid(compiled) || jsonDocumentContainsToken(compiled) {
		return nil, ErrCardCompileFailed
	}
	occurredAt := request.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = m.now().UTC()
	}
	transitioned, err := m.store.TransitionSurface(
		ctx,
		TransitionSurfaceRequest{
			SurfaceID:        request.Surface.ID,
			ExpectedRevision: request.Surface.Revision,
			From:             request.Surface.Status, To: request.To,
			CompiledJSONRedacted: string(compiled),
			ActionID:             request.ActionID, ActorOpenID: request.ActorOpenID,
			SourceRef: request.SourceRef, OccurredAt: occurredAt,
		},
	)
	if err != nil {
		return nil, err
	}
	if transitioned.PatchStatus == PatchStatusIdle {
		return transitioned, nil
	}
	if err := m.worker.Process(
		ctx,
		transitioned.ID,
		transitioned.Revision,
	); err != nil {
		return transitioned, err
	}
	transitioned.PatchStatus = PatchStatusIdle
	transitioned.PatchWorkerID = ""
	transitioned.PatchLeaseExpiresAt = time.Time{}
	transitioned.LastError = ""
	return transitioned, nil
}

func lifecycleStateForStatus(status SurfaceStatus) (LifecycleState, error) {
	switch status {
	case SurfaceStatusSubmitted:
		return LifecycleSubmitted, nil
	case SurfaceStatusProcessing:
		return LifecycleProcessing, nil
	case SurfaceStatusResolved:
		return LifecycleResolved, nil
	case SurfaceStatusCancelled:
		return LifecycleCancelled, nil
	case SurfaceStatusExpired:
		return LifecycleExpired, nil
	case SurfaceStatusFailed:
		return LifecycleFailed, nil
	default:
		return "", fmt.Errorf("surface status %q has no terminal renderer", status)
	}
}

func jsonDocumentContainsToken(document json.RawMessage) bool {
	var value any
	if json.Unmarshal(document, &value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if strings.EqualFold(key, "token") || visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}
