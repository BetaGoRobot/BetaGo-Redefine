package handlers

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestImageAddSuppressesReactionDuringRuntimeCapabilityExecution(t *testing.T) {
	reactionCalled := false
	createCalled := false

	originalReaction := imageAddReactionAsync
	originalCreate := imageCreateFunc
	t.Cleanup(func() {
		imageAddReactionAsync = originalReaction
		imageCreateFunc = originalCreate
	})

	imageAddReactionAsync = func(ctx context.Context, reactionType, msgID string) error {
		reactionCalled = true
		return nil
	}
	imageCreateFunc = func(ctx context.Context, msgID, chatID, imgKey, msgType string) error {
		createCalled = true
		return nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	meta := &xhandler.BaseMetaData{}
	err := ImageAdd.Handle(
		runtimecontext.WithCapabilityExecution(context.Background(), "image_add"),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		meta,
		ImageAddArgs{ImgKey: "img_test"},
	)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !createCalled {
		t.Fatal("expected create path to execute")
	}
	if reactionCalled {
		t.Fatal("reaction should be suppressed during runtime capability execution")
	}
}

func TestImageDeleteSuppressesReactionDuringRuntimeCapabilityExecution(t *testing.T) {
	reactionCalled := false
	deleteCalled := false

	originalReaction := imageAddReactionAsync
	originalDelete := imageDeleteByKeyFunc
	t.Cleanup(func() {
		imageAddReactionAsync = originalReaction
		imageDeleteByKeyFunc = originalDelete
	})

	imageAddReactionAsync = func(ctx context.Context, reactionType, msgID string) error {
		reactionCalled = true
		return nil
	}
	imageDeleteByKeyFunc = func(ctx context.Context, chatID, imgKey, msgType string) error {
		deleteCalled = true
		return nil
	}

	msgID := "om_test"
	chatID := "oc_test"
	meta := &xhandler.BaseMetaData{}
	err := ImageDelete.Handle(
		runtimecontext.WithCapabilityExecution(context.Background(), "image_delete"),
		&larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Message: &larkim.EventMessage{
					MessageId: &msgID,
					ChatId:    &chatID,
				},
			},
		},
		meta,
		ImageDeleteArgs{ImgKey: "img_test"},
	)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !deleteCalled {
		t.Fatal("expected delete path to execute")
	}
	if reactionCalled {
		t.Fatal("reaction should be suppressed during runtime capability execution")
	}
}
