package recording

import (
	"context"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type contextCaptureSubmitter struct {
	contextErr error
}

func (s *contextCaptureSubmitter) Submit(
	ctx context.Context,
	_ string,
	_ func(context.Context) error,
) error {
	s.contextErr = ctx.Err()
	return nil
}

func TestCollectMessageSubmitsWithDurableContext(t *testing.T) {
	previous := getBackgroundSubmitter()
	t.Cleanup(func() {
		SetBackgroundSubmitter(previous)
	})
	submitter := &contextCaptureSubmitter{}
	SetBackgroundSubmitter(submitter)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	messageID := "om_context"
	CollectMessage(ctx, &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId: &messageID,
			},
		},
	}, nil)

	if submitter.contextErr != nil {
		t.Fatalf("CollectMessage() submitted canceled context: %v", submitter.contextErr)
	}
}
