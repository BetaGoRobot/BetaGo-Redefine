package larkmsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/runtimecontext"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/lark_dal/larkmsg/larkcard"
	redis_dal "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/redis"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	agentMentionElementID      = "agt_root_mention"
	agentThoughtPanelElementID = "agt_thought_panel"
	agentThoughtElementID      = "agt_thought"
	agentReplyElementID        = "agt_reply"

	agentThoughtPlaceholder = "<font color='grey'>思考中...</font>"
	agentReplyPlaceholder   = "<font color='grey'>生成中...</font>"

	cardStreamingSequenceTTL = 24 * time.Hour
)

type AgentStreamingCardRefs struct {
	MessageID string
	CardID    string
}

type AgentStreamingCardOptions struct {
	MentionOpenID string
}

var (
	sendAgentStreamingCreateCardFunc = sendAgentStreamingCreateCard
	patchAgentStreamingCardFunc      = patchAgentStreamingCard
	updateAgentCardElementFunc       = updateAgentCardElement

	localCardStreamingSequences sync.Map
)

func newAgentStreamingCard(opts AgentStreamingCardOptions) RawCard {
	elements := make([]any, 0, 5)
	if mentionOpenID := strings.TrimSpace(opts.MentionOpenID); mentionOpenID != "" {
		elements = append(elements, agentStreamingMarkdown(AtUserMD(mentionOpenID), agentMentionElementID))
		elements = append(elements, Divider())
	}
	elements = append(elements,
		CollapsiblePanel(
			"思考过程",
			[]any{agentStreamingMarkdown(agentThoughtPlaceholder, agentThoughtElementID)},
			CollapsiblePanelOptions{
				ElementID:       agentThoughtPanelElementID,
				Expanded:        false,
				Padding:         "8px",
				VerticalSpacing: "4px",
			},
		),
		Divider(),
		agentStreamingMarkdown(agentReplyPlaceholder, agentReplyElementID),
	)
	return NewCardV2("BetaGo", elements, CardV2Options{
		HeaderTemplate:  "wathet",
		VerticalSpacing: "8px",
		Padding:         "12px",
	})
}

func agentStreamingMarkdown(content, elementID string) map[string]any {
	element := map[string]any{
		"tag":     "markdown",
		"content": content,
	}
	if normalized := normalizeElementID(elementID); normalized != "" {
		element["element_id"] = normalized
	}
	return element
}

func formatAgentThoughtContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return agentThoughtPlaceholder
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = "> " + strings.TrimRight(line, "\r")
	}
	return strings.Join(lines, "\n")
}

func formatAgentReplyContent(data *ark_dal.ModelStreamRespReasoning) string {
	if data == nil {
		return agentReplyPlaceholder
	}
	if reply := strings.TrimSpace(data.ContentStruct.Reply); reply != "" {
		return reply
	}
	if content := strings.TrimSpace(data.Content); content != "" {
		return content
	}
	return agentReplyPlaceholder
}

func sendAgentStreamingReplyCard(
	ctx context.Context,
	msg *larkim.EventMessage,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
	replyInThread bool,
	options ...AgentStreamingCardOptions,
) (AgentStreamingCardRefs, error) {
	cardID, err := createAgentStreamingCardEntity(ctx, mergeAgentStreamingCardOptions(options...))
	if err != nil {
		return AgentStreamingCardRefs{}, err
	}
	resp, err := ReplyMsgRawContentType(
		ctx,
		*msg.MessageId,
		larkim.MsgTypeInteractive,
		larkcard.NewCardEntityContent(cardID).String(),
		"_agent_stream",
		replyInThread,
	)
	if err != nil {
		return AgentStreamingCardRefs{CardID: cardID}, err
	}
	if resp == nil || !resp.Success() {
		if resp == nil {
			return AgentStreamingCardRefs{CardID: cardID}, errors.New("empty reply response for streaming card")
		}
		return AgentStreamingCardRefs{CardID: cardID}, errors.New(resp.Error())
	}
	go RecordReplyMessage2Opensearch(ctx, resp)
	refs := AgentStreamingCardRefs{CardID: cardID}
	if resp.Data != nil && resp.Data.MessageId != nil {
		refs.MessageID = *resp.Data.MessageId
	}
	runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
	if err := streamAgentCardContent(ctx, cardID, msgSeq); err != nil {
		return refs, err
	}
	return refs, nil
}

func sendAgentStreamingCreateCard(
	ctx context.Context,
	msg *larkim.EventMessage,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
	options ...AgentStreamingCardOptions,
) (AgentStreamingCardRefs, error) {
	cardID, err := createAgentStreamingCardEntity(ctx, mergeAgentStreamingCardOptions(options...))
	if err != nil {
		return AgentStreamingCardRefs{}, err
	}
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(
			larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(*msg.ChatId).
				MsgType(larkim.MsgTypeInteractive).
				Content(larkcard.NewCardEntityContent(cardID).String()).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Im.V1.Message.Create(ctx, req)
	if err != nil {
		return AgentStreamingCardRefs{CardID: cardID}, err
	}
	if !resp.Success() {
		return AgentStreamingCardRefs{CardID: cardID}, errors.New(resp.Error())
	}
	RecordMessage2Opensearch(ctx, resp)
	refs := AgentStreamingCardRefs{CardID: cardID}
	if resp.Data != nil && resp.Data.MessageId != nil {
		refs.MessageID = *resp.Data.MessageId
	}
	runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
	if err := streamAgentCardContent(ctx, cardID, msgSeq); err != nil {
		return refs, err
	}
	return refs, nil
}

func PatchAgentStreamingCardWithRefs(
	ctx context.Context,
	refs AgentStreamingCardRefs,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
) (AgentStreamingCardRefs, error) {
	return patchAgentStreamingCardFunc(ctx, refs, msgSeq)
}

func patchAgentStreamingCard(
	ctx context.Context,
	refs AgentStreamingCardRefs,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
) (AgentStreamingCardRefs, error) {
	if strings.TrimSpace(refs.CardID) == "" {
		return refs, errors.New("card id is required")
	}
	runtimecontext.RecordActiveAgenticReplyTarget(ctx, refs.MessageID, refs.CardID)
	if err := streamAgentCardContent(ctx, strings.TrimSpace(refs.CardID), msgSeq); err != nil {
		return refs, err
	}
	return refs, nil
}

func createAgentStreamingCardEntity(ctx context.Context, opts AgentStreamingCardOptions) (string, error) {
	raw, err := json.Marshal(newAgentStreamingCard(opts))
	if err != nil {
		return "", err
	}
	return createCardEntity(ctx, cardKitTypeCardJSON, string(raw))
}

func mergeAgentStreamingCardOptions(options ...AgentStreamingCardOptions) AgentStreamingCardOptions {
	merged := AgentStreamingCardOptions{}
	for _, option := range options {
		if strings.TrimSpace(merged.MentionOpenID) == "" {
			merged.MentionOpenID = strings.TrimSpace(option.MentionOpenID)
		}
	}
	return merged
}

func streamAgentCardContent(ctx context.Context, cardID string, msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning]) error {
	initialSequence, err := nextCardStreamingSequence(ctx, cardID)
	if err != nil {
		return err
	}
	if err := setCardStreamingMode(ctx, cardID, true, initialSequence); err != nil {
		return err
	}

	lastSequence, err := updateAgentStreamingCardWithSource(ctx, cardID, msgSeq, initialSequence, func() (int, error) {
		return nextCardStreamingSequence(ctx, cardID)
	})
	if err != nil {
		return err
	}
	finalSequence, err := nextCardStreamingSequence(ctx, cardID)
	if err != nil {
		return err
	}
	if finalSequence <= lastSequence {
		finalSequence = lastSequence + 1
	}
	return setCardStreamingMode(ctx, cardID, false, finalSequence)
}

func setCardStreamingMode(ctx context.Context, cardID string, enabled bool, sequence int) error {
	settings := larkcard.DisableCardStreaming()
	if enabled {
		settings = larkcard.EnableCardStreaming()
	}
	req := larkcardkit.NewSettingsCardReqBuilder().
		CardId(cardID).
		Body(
			larkcardkit.NewSettingsCardReqBodyBuilder().
				Settings(settings.String()).
				Uuid(cardStreamingUUID(cardID, "settings", sequence)).
				Sequence(sequence).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.Card.Settings(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.CodeError.Error())
	}
	return nil
}

func updateAgentStreamingCard(
	ctx context.Context,
	cardID string,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
	startSequence int,
) (int, error) {
	nextSequenceValue := startSequence
	return updateAgentStreamingCardWithSource(ctx, cardID, msgSeq, startSequence, func() (int, error) {
		nextSequenceValue++
		return nextSequenceValue, nil
	})
}

func updateAgentStreamingCardWithSource(
	ctx context.Context,
	cardID string,
	msgSeq iter.Seq[*ark_dal.ModelStreamRespReasoning],
	startSequence int,
	nextSequence func() (int, error),
) (int, error) {
	sequence := startSequence
	lastFlush := time.Now()
	pending := map[string]string{}
	var thoughtBuilder strings.Builder
	var replyBuilder strings.Builder

	flush := func() error {
		order := []string{agentThoughtElementID, agentReplyElementID}
		for _, elementID := range order {
			content, ok := pending[elementID]
			if !ok || strings.TrimSpace(content) == "" {
				continue
			}
			next, err := nextSequence()
			if err != nil {
				return err
			}
			sequence = next
			if err := updateAgentCardElementFunc(ctx, cardID, elementID, content, sequence); err != nil {
				return err
			}
		}
		clear(pending)
		lastFlush = time.Now()
		return nil
	}

	for data := range msgSeq {
		if data == nil {
			continue
		}

		if data.ReasoningContent != "" {
			thoughtBuilder.WriteString(data.ReasoningContent)
			pending[agentThoughtElementID] = formatAgentThoughtContent(thoughtBuilder.String())
		}

		switch {
		case strings.TrimSpace(data.ContentStruct.Reply) != "":
			pending[agentReplyElementID] = formatAgentReplyContent(data)
		case data.Content != "":
			replyBuilder.WriteString(data.Content)
			// pending[agentReplyElementID] = formatAgentReplyContent(&ark_dal.ModelStreamRespReasoning{
			// 	Content: replyBuilder.String(),
			// })
		}

		if time.Since(lastFlush) >= 20*time.Millisecond {
			if err := flush(); err != nil {
				return sequence, err
			}
		}
	}

	if len(pending) > 0 {
		if err := flush(); err != nil {
			return sequence, err
		}
	}
	return sequence, nil
}

func updateAgentCardElement(ctx context.Context, cardID, elementID, content string, sequence int) error {
	req := larkcardkit.NewContentCardElementReqBuilder().
		CardId(cardID).
		ElementId(elementID).
		Body(
			larkcardkit.NewContentCardElementReqBodyBuilder().
				Content(content).
				Uuid(cardStreamingUUID(cardID, elementID, sequence)).
				Sequence(sequence).
				Build(),
		).
		Build()
	resp, err := lark_dal.Client().Cardkit.V1.CardElement.Content(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return errors.New(resp.CodeError.Error())
	}
	return nil
}

func cardStreamingUUID(cardID, elementID string, sequence int) string {
	return fmt.Sprintf("%s-%s-%d", cardID, elementID, sequence)
}

func nextCardStreamingSequence(ctx context.Context, cardID string) (int, error) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return 0, errors.New("card id is required")
	}

	client := redis_dal.GetRedisClient()
	if client != nil {
		key := cardStreamingSequenceRedisKey(cardID)
		next, err := client.Incr(ctx, key).Result()
		if err == nil {
			if expireErr := client.Expire(ctx, key, cardStreamingSequenceTTL).Err(); expireErr != nil && !errors.Is(expireErr, redis.Nil) {
				logs.L().Ctx(ctx).Warn("extend card streaming sequence ttl failed",
					zap.String("card_id", cardID),
					zap.Error(expireErr),
				)
			}
			return int(next), nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		logs.L().Ctx(ctx).Warn("allocate card streaming sequence from redis failed; falling back to local state",
			zap.String("card_id", cardID),
			zap.Error(err),
		)
	}

	return nextLocalCardStreamingSequence(cardID), nil
}

func cardStreamingSequenceRedisKey(cardID string) string {
	return botidentity.Current().NamespaceKey("larkmsg", "agent_stream_sequence", strings.TrimSpace(cardID))
}

func nextLocalCardStreamingSequence(cardID string) int {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return 0
	}
	value, _ := localCardStreamingSequences.LoadOrStore(cardID, &atomic.Int64{})
	counter, _ := value.(*atomic.Int64)
	if counter == nil {
		counter = &atomic.Int64{}
		localCardStreamingSequences.Store(cardID, counter)
	}
	return int(counter.Add(1))
}
