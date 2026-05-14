package larkmsg

import (
	"context"
	"encoding/json"
	"iter"
	"sort"
	"strings"
	"sync"
	"testing"

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
	updateStarted := make(chan struct{}, 2)
	releaseUpdate := make(chan struct{})

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
		updateStarted <- struct{}{}
		<-releaseUpdate
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
		return nil
	}
	streamingSetCardStreaming = func(ctx context.Context, update streamingSettingsUpdate) error {
		mu.Lock()
		settings = append(settings, update)
		mu.Unlock()
		return nil
	}

	msgID := "origin_msg"
	errCh := make(chan error, 1)
	go func() {
		errCh <- SendAndReplyStreamingCard(context.Background(), &larkim.EventMessage{MessageId: &msgID}, streamOf(
			&ark_dal.ModelStreamRespReasoning{Content: "first"},
			&ark_dal.ModelStreamRespReasoning{ContentStruct: ark_dal.ContentStruct{Reply: "second"}},
			&ark_dal.ModelStreamRespReasoning{Content: "third"},
		), true)
	}()

	<-updateStarted
	<-updateStarted

	select {
	case err := <-errCh:
		t.Fatalf("stream returned before in-flight updates were released: %v", err)
	default:
	}

	close(releaseUpdate)
	if err := <-errCh; err != nil {
		t.Fatalf("SendAndReplyStreamingCard() error = %v", err)
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
		t.Fatalf("expected initial card streaming mode enabled: %#v", createdCard["config"])
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 2 {
		t.Fatalf("expected two content updates after initial card, got %#v", updates)
	}
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].Sequence < updates[j].Sequence
	})
	if updates[0].Sequence >= updates[1].Sequence {
		t.Fatalf("expected increasing update sequences: %#v", updates)
	}
	if updates[0].CardID != "card_123" || updates[0].ElementID != streamingReplyElementID || updates[0].Content != "second" {
		t.Fatalf("unexpected first update: %#v", updates[0])
	}
	if updates[1].Content != "third" {
		t.Fatalf("unexpected second update: %#v", updates[1])
	}
	if len(settings) != 1 {
		t.Fatalf("expected final streaming settings update, got %#v", settings)
	}
	if settings[0].StreamingMode {
		t.Fatalf("expected final streaming mode disabled")
	}
	if settings[0].Sequence <= updates[1].Sequence {
		t.Fatalf("expected final settings sequence after content updates: settings=%#v updates=%#v", settings, updates)
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
