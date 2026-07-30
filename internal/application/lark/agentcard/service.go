package agentcard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrSurfaceDeliveryFailed    = errors.New("agent card delivery failed")
	ErrSurfaceDeliveryAmbiguous = errors.New("agent card delivery result is ambiguous")
	ErrSurfaceDeliveryPending   = errors.New("agent card delivery is pending reconciliation")
)

type SurfaceClient interface {
	ReplyCard(context.Context, string, any) (string, error)
	CreateCard(context.Context, string, any) (string, error)
	PatchCard(context.Context, string, any) error
}

type InteractionBinder interface {
	BindAndBegin(context.Context, BindRequest) (*BindResult, error)
}

type DeliveryStore interface {
	MarkSurfaceSent(context.Context, MarkSurfaceSentRequest) (*CardSurface, error)
	MarkSurfaceSendFailed(context.Context, MarkSurfaceSendFailedRequest) (*CardSurface, error)
	MarkSurfaceSendUncertain(context.Context, MarkSurfaceSendUncertainRequest) (*CardSurface, error)
}

type Service struct {
	binder InteractionBinder
	store  DeliveryStore
	client SurfaceClient
	now    func() time.Time
}

func NewService(
	binder InteractionBinder,
	store DeliveryStore,
	client SurfaceClient,
) *Service {
	return &Service{
		binder: binder, store: store, client: client, now: time.Now,
	}
}

func (s *Service) ComposeAndSend(
	ctx context.Context,
	request BindRequest,
) (*CardSurface, error) {
	if s == nil || s.binder == nil || s.store == nil || s.client == nil {
		return nil, errors.New("agent card delivery service is not configured")
	}
	bound, err := s.binder.BindAndBegin(ctx, request)
	if err != nil {
		return nil, err
	}
	if bound == nil || bound.Surface == nil ||
		!json.Valid(bound.CompiledJSON) {
		return nil, ErrSurfaceDeliveryFailed
	}
	surface := bound.Surface
	if surface.Status == SurfaceStatusSent && surface.MessageID != "" {
		return surface, nil
	}
	if surface.Status != SurfaceStatusDraft {
		return nil, ErrCardConflict
	}

	var messageID string
	card := any(append(json.RawMessage(nil), bound.CompiledJSON...))
	if surface.ReplyToMessageID != "" {
		messageID, err = s.client.ReplyCard(
			ctx,
			surface.ReplyToMessageID,
			card,
		)
	} else {
		messageID, err = s.client.CreateCard(ctx, surface.ChatID, card)
	}
	sourceRef := "delivery:" + request.IdempotencyKey
	now := s.now().UTC()
	if err != nil {
		if errors.Is(err, ErrSurfaceDeliveryAmbiguous) {
			_, persistErr := s.store.MarkSurfaceSendUncertain(
				ctx,
				MarkSurfaceSendUncertainRequest{
					SurfaceID: surface.ID, ExpectedRevision: surface.Revision,
					SourceRef: sourceRef, ErrorCode: "ambiguous_transport",
					ObservedAt: now,
				},
			)
			if persistErr != nil {
				return nil, fmt.Errorf(
					"persist ambiguous agent card delivery: %w",
					persistErr,
				)
			}
			return nil, ErrSurfaceDeliveryPending
		}
		if _, persistErr := s.store.MarkSurfaceSendFailed(
			ctx,
			MarkSurfaceSendFailedRequest{
				SurfaceID: surface.ID, ExpectedRevision: surface.Revision,
				SourceRef: sourceRef, ErrorCode: "delivery_rejected",
				FailedAt: now,
			},
		); persistErr != nil {
			return nil, fmt.Errorf(
				"persist failed agent card delivery: %w",
				persistErr,
			)
		}
		return nil, ErrSurfaceDeliveryFailed
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		if _, persistErr := s.store.MarkSurfaceSendFailed(
			ctx,
			MarkSurfaceSendFailedRequest{
				SurfaceID: surface.ID, ExpectedRevision: surface.Revision,
				SourceRef: sourceRef, ErrorCode: "missing_message_id",
				FailedAt: now,
			},
		); persistErr != nil {
			return nil, fmt.Errorf(
				"persist missing agent card message id: %w",
				persistErr,
			)
		}
		return nil, ErrSurfaceDeliveryFailed
	}
	sent, err := s.store.MarkSurfaceSent(ctx, MarkSurfaceSentRequest{
		SurfaceID: surface.ID, ExpectedRevision: surface.Revision,
		MessageID: messageID, SourceRef: sourceRef, SentAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("persist sent agent card surface: %w", err)
	}
	return sent, nil
}
