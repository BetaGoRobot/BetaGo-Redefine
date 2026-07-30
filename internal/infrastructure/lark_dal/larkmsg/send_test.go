package larkmsg

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestSendAndReplyStreamingCardUsesCardKitSequenceUpdates(t *testing.T) {
	originalCreate := streamingCreateCardEntity
	originalReply := streamingReplyCardEntity
	originalUpdate := streamingUpdateCardContent
	originalSetStreaming := streamingSetCardStreaming
	t.Cleanup(func() {
		streamingCreateCardEntity = originalCreate
		streamingReplyCardEntity = originalReply
		streamingUpdateCardContent = originalUpdate
		streamingSetCardStreaming = originalSetStreaming
	})

	var (
		createdCard map[string]any
		replyCalls  int
		updates     []streamingContentUpdate
		settings    []streamingSettingsUpdate
		mu          sync.Mutex
	)
	updateStarted := make(chan struct{}, 8)
	releaseUpdate := make(chan struct{})

	// 用于断言 settings 调用发生在所有 content update 全部返回之后。
	var (
		updateDispatched   atomic.Int64
		updateCompleted    atomic.Int64
		settingSawInFlight int64
	)

	streamingCreateCardEntity = func(ctx context.Context, cardData any) (string, error) {
		raw, err := json.Marshal(cardData)
		if err != nil {
			t.Fatalf("marshal card: %v", err)
		}
		if err := json.Unmarshal(raw, &createdCard); err != nil {
			t.Fatalf("unmarshal card: %v", err)
		}
		return "card_123", nil
	}
	streamingReplyCardEntity = func(ctx context.Context, msgID, cardID, suffix string, replyInThread bool) (*larkim.ReplyMessageResp, error) {
		replyCalls++
		if msgID != "origin_msg" {
			t.Fatalf("unexpected msg id: %s", msgID)
		}
		if cardID != "card_123" {
			t.Fatalf("unexpected card id: %s", cardID)
		}
		if !replyInThread {
			t.Fatalf("expected thread reply")
		}
		replyMsgID := "reply_msg"
		return &larkim.ReplyMessageResp{
			CodeError: larkcore.CodeError{Code: 0},
			Data:      &larkim.ReplyMessageRespData{MessageId: &replyMsgID},
		}, nil
	}
	streamingUpdateCardContent = func(ctx context.Context, update streamingContentUpdate) error {
		updateDispatched.Add(1)
		updateStarted <- struct{}{}
		<-releaseUpdate
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
		updateCompleted.Add(1)
		return nil
	}
	streamingSetCardStreaming = func(ctx context.Context, update streamingSettingsUpdate) error {
		// 关键断言：settings 被调用时，所有已 dispatch 的 content update 必须已经完成。
		dispatched := updateDispatched.Load()
		completed := updateCompleted.Load()
		if dispatched != completed {
			settingSawInFlight = dispatched - completed
		}
		mu.Lock()
		settings = append(settings, update)
		mu.Unlock()
		return nil
	}

	msgID := "origin_msg"
	type sendResult struct {
		messageID string
		err       error
	}
	resultCh := make(chan sendResult, 1)
	go func() {
		messageID, err := SendAndReplyStreamingCardReturning(context.Background(), &larkim.EventMessage{MessageId: &msgID}, streamOf(
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "first"}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "second"}},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "third"}},
		), true)
		resultCh <- sendResult{messageID: messageID, err: err}
	}()

	<-updateStarted

	select {
	case result := <-resultCh:
		t.Fatalf("stream returned before in-flight updates were released: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseUpdate)
	result := <-resultCh
	if result.err != nil {
		err := result.err
		t.Fatalf("SendAndReplyStreamingCard() error = %v", err)
	}
	if result.messageID != "reply_msg" {
		t.Fatalf("SendAndReplyStreamingCardReturning() messageID = %q, want %q", result.messageID, "reply_msg")
	}

	if settingSawInFlight != 0 {
		t.Fatalf("settings called while %d content update(s) still in flight; streaming=false may race ahead of final content", settingSawInFlight)
	}
	if replyCalls != 1 {
		t.Fatalf("expected one reply call, got %d", replyCalls)
	}
	body, ok := createdCard["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected card body: %#v", createdCard)
	}
	elements, ok := body["elements"].([]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("expected one streaming element: %#v", body["elements"])
	}
	element, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatalf("expected element map: %#v", elements[0])
	}
	if got := element["element_id"]; got != streamingReplyElementID {
		t.Fatalf("expected streaming element id %q, got %#v", streamingReplyElementID, got)
	}
	if got := element["content"]; got != "first" {
		t.Fatalf("expected initial content first, got %#v", got)
	}
	config, ok := createdCard["config"].(map[string]any)
	if !ok || config["streaming_mode"] != true {
		t.Fatalf("expected initial card streaming mode enabled: %#v", createdCard)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 1 {
		t.Fatalf("expected at least one content update after initial card, got %#v", updates)
	}
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].Sequence < updates[j].Sequence
	})
	for i := 1; i < len(updates); i++ {
		if updates[i-1].Sequence >= updates[i].Sequence {
			t.Fatalf("expected increasing update sequences: %#v", updates)
		}
	}
	last := updates[len(updates)-1]
	if last.CardID != "card_123" || last.ElementID != streamingReplyElementID || last.Content != "third" {
		t.Fatalf("expected last update to carry latest element (third), got %#v", last)
	}
	if len(settings) != 1 {
		t.Fatalf("expected final streaming settings update, got %#v", settings)
	}
	if settings[0].StreamingMode {
		t.Fatalf("expected final streaming mode disabled")
	}
	if settings[0].Sequence <= last.Sequence {
		t.Fatalf("expected final settings sequence after content updates: settings=%#v updates=%#v", settings, updates)
	}
}

func TestSendAndUpdateStreamingCardReturningReturnsMessageID(t *testing.T) {
	originalCreate := streamingCreateCardEntity
	originalReply := streamingReplyCardEntity
	originalSetStreaming := streamingSetCardStreaming
	t.Cleanup(func() {
		streamingCreateCardEntity = originalCreate
		streamingReplyCardEntity = originalReply
		streamingSetCardStreaming = originalSetStreaming
	})

	streamingCreateCardEntity = func(context.Context, any) (string, error) {
		return "card_update", nil
	}
	streamingReplyCardEntity = func(_ context.Context, _, cardID, _ string, replyInThread bool) (*larkim.ReplyMessageResp, error) {
		if cardID != "card_update" {
			t.Fatalf("cardID = %q, want %q", cardID, "card_update")
		}
		if replyInThread {
			t.Fatal("SendAndUpdateStreamingCardReturning() unexpectedly replied in thread")
		}
		messageID := "updated_reply_msg"
		return &larkim.ReplyMessageResp{
			CodeError: larkcore.CodeError{Code: 0},
			Data:      &larkim.ReplyMessageRespData{MessageId: &messageID},
		}, nil
	}
	streamingSetCardStreaming = func(context.Context, streamingSettingsUpdate) error {
		return nil
	}

	originMessageID := "origin_msg"
	messageID, err := SendAndUpdateStreamingCardReturning(
		context.Background(),
		&larkim.EventMessage{MessageId: &originMessageID},
		streamOf(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{Reply: "complete"},
		}),
	)
	if err != nil {
		t.Fatalf("SendAndUpdateStreamingCardReturning() error = %v", err)
	}
	if messageID != "updated_reply_msg" {
		t.Fatalf("SendAndUpdateStreamingCardReturning() messageID = %q, want %q", messageID, "updated_reply_msg")
	}
}

func TestSendAndReplyStreamingCardReturningSkipsEmptyStream(t *testing.T) {
	originMessageID := "origin_msg"
	messageID, err := SendAndReplyStreamingCardReturning(
		context.Background(),
		&larkim.EventMessage{MessageId: &originMessageID},
		streamOf(
			nil,
			&ark_dal.ModelStreamRespReasoning{},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "  "}},
		),
		true,
	)
	if err != nil {
		t.Fatalf("SendAndReplyStreamingCardReturning() error = %v", err)
	}
	if messageID != "" {
		t.Fatalf("SendAndReplyStreamingCardReturning() messageID = %q, want empty", messageID)
	}
}

func TestCreateAndReplyCardReturnsLarkMessageIDForInitialAndFinalCards(t *testing.T) {
	originalCreate := streamingCreateCardEntity
	originalReply := streamingReplyCardEntity
	t.Cleanup(func() {
		streamingCreateCardEntity = originalCreate
		streamingReplyCardEntity = originalReply
	})

	streamingCreateCardEntity = func(context.Context, any) (string, error) {
		return "card_123", nil
	}

	tests := []struct {
		name          string
		isFinal       bool
		wantSuffix    string
		wantMessageID string
	}{
		{name: "initial", wantSuffix: "_streaming_reply", wantMessageID: "initial_message"},
		{name: "final", isFinal: true, wantSuffix: "_streaming_reply_final", wantMessageID: "final_message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamingReplyCardEntity = func(_ context.Context, _, cardID, suffix string, _ bool) (*larkim.ReplyMessageResp, error) {
				if cardID != "card_123" {
					t.Fatalf("cardID = %q, want %q", cardID, "card_123")
				}
				if suffix != tt.wantSuffix {
					t.Fatalf("suffix = %q, want %q", suffix, tt.wantSuffix)
				}
				messageID := tt.wantMessageID
				return &larkim.ReplyMessageResp{
					CodeError: larkcore.CodeError{Code: 0},
					Data:      &larkim.ReplyMessageRespData{MessageId: &messageID},
				}, nil
			}

			pusher := newStreamingCardPusher(context.Background())
			defer pusher.Stop()
			originMessageID := "origin_msg"
			messageID, err := createAndReplyCard(
				context.Background(),
				&larkim.EventMessage{MessageId: &originMessageID},
				pusher,
				"reply",
				true,
				tt.isFinal,
			)
			if err != nil {
				t.Fatalf("createAndReplyCard() error = %v", err)
			}
			if messageID != tt.wantMessageID {
				t.Fatalf("createAndReplyCard() messageID = %q, want %q", messageID, tt.wantMessageID)
			}
		})
	}
}

func TestSendAndReplyStreamingCardReturningDoesNotFabricateMessageIDOnError(t *testing.T) {
	originalCreate := streamingCreateCardEntity
	t.Cleanup(func() {
		streamingCreateCardEntity = originalCreate
	})

	wantErr := errors.New("create card failed")
	streamingCreateCardEntity = func(context.Context, any) (string, error) {
		return "", wantErr
	}

	originMessageID := "origin_msg"
	messageID, err := SendAndReplyStreamingCardReturning(
		context.Background(),
		&larkim.EventMessage{MessageId: &originMessageID},
		streamOf(&ark_dal.ModelStreamRespReasoning{
			ContentStruct: ark_dal.ContentStruct{Reply: "reply"},
		}),
		true,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendAndReplyStreamingCardReturning() error = %v, want %v", err, wantErr)
	}
	if messageID != "" {
		t.Fatalf("SendAndReplyStreamingCardReturning() messageID = %q, want empty on error", messageID)
	}
}

func TestStreamingCardLegacyAPIsKeepErrorOnlySignatures(t *testing.T) {
	var reply func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning], bool) error = SendAndReplyStreamingCard
	var update func(context.Context, *larkim.EventMessage, iter.Seq[*ark_dal.ModelStreamRespReasoning]) error = SendAndUpdateStreamingCard
	if reply == nil || update == nil {
		t.Fatal("legacy streaming card API is nil")
	}
}

func streamOf(items ...*ark_dal.ModelStreamRespReasoning) iter.Seq[*ark_dal.ModelStreamRespReasoning] {
	return func(yield func(*ark_dal.ModelStreamRespReasoning) bool) {
		for _, item := range items {
			if !yield(item) {
				return
			}
		}
	}
}

func TestStreamingChunkTextPrefersStructuredReply(t *testing.T) {
	data := &ark_dal.ModelStreamRespReasoning{
		Content: " raw ",
		ContentStruct: ark_dal.ContentStruct{
			Reply: " structured ",
		},
	}

	if got := streamingChunkText(data); !strings.EqualFold(got, "structured") {
		t.Fatalf("streamingChunkText() = %q", got)
	}
}
