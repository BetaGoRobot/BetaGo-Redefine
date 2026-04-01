package replay

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/history"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/xmodel"
	"github.com/defensestation/osquery"
)

type MessageShape string

const (
	MessageShapeMention             MessageShape = "mention"
	MessageShapeReplyToBot          MessageShape = "reply_to_bot"
	MessageShapeCommand             MessageShape = "command"
	MessageShapeAmbientGroupMessage MessageShape = "ambient_group_message"
)

type SampleFilterOptions struct {
	ChatID             string
	Days               int
	Limit              int
	AllowedShapes      []MessageShape
	RequireQuestion    bool
	RequireLongMessage bool
	RequireLink        bool
	RequireAttachment  bool
	Keyword            string
}

type ReplaySample struct {
	MessageID       string       `json:"message_id"`
	ChatID          string       `json:"chat_id"`
	CreateTime      string       `json:"create_time"`
	MessageType     string       `json:"message_type"`
	RawMessage      string       `json:"raw_message"`
	PrimaryShape    MessageShape `json:"primary_shape"`
	ContentFeatures []string     `json:"content_features,omitempty"`
}

type (
	sampleMessageLoader      func(context.Context, SampleFilterOptions) ([]*xmodel.MessageIndex, error)
	sampleMentionDetector    func(*xmodel.MessageIndex) bool
	sampleReplyToBotDetector func(context.Context, *xmodel.MessageIndex) bool
)

type SampleSelectorService struct {
	loadMessages sampleMessageLoader
	isMention    sampleMentionDetector
	isReplyToBot sampleReplyToBotDetector
}

func (s SampleSelectorService) Select(ctx context.Context, options SampleFilterOptions) ([]ReplaySample, error) {
	options = normalizeSampleFilterOptions(options)
	messages, err := s.messageLoader()(ctx, options)
	if err != nil {
		return nil, err
	}
	samples := make([]ReplaySample, 0, len(messages))
	for _, item := range messages {
		sample, ok := s.toReplaySample(ctx, item)
		if !ok {
			continue
		}
		if !matchesSampleFilters(sample, options) {
			continue
		}
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(i, j int) bool {
		left := parseCatalogTimeValue(samples[i].CreateTime)
		right := parseCatalogTimeValue(samples[j].CreateTime)
		if left.Equal(right) {
			return samples[i].MessageID < samples[j].MessageID
		}
		return left.After(right)
	})
	if len(samples) > options.Limit {
		samples = samples[:options.Limit]
	}
	return samples, nil
}

func (s SampleSelectorService) toReplaySample(ctx context.Context, item *xmodel.MessageIndex) (ReplaySample, bool) {
	if item == nil || item.MessageLog == nil {
		return ReplaySample{}, false
	}
	messageID := strings.TrimSpace(item.MessageID)
	chatID := strings.TrimSpace(item.ChatID)
	if messageID == "" || chatID == "" {
		return ReplaySample{}, false
	}
	text := firstNonEmpty(item.RawMessage, item.Content)
	shape := s.classifyMessageShape(ctx, item)
	features := detectContentFeatures(text, strings.TrimSpace(item.MessageType))
	return ReplaySample{
		MessageID:       messageID,
		ChatID:          chatID,
		CreateTime:      firstNonEmpty(strings.TrimSpace(item.CreateTime), formatCatalogTime(item.CreatedAt)),
		MessageType:     strings.TrimSpace(item.MessageType),
		RawMessage:      text,
		PrimaryShape:    shape,
		ContentFeatures: features,
	}, true
}

func (s SampleSelectorService) classifyMessageShape(ctx context.Context, item *xmodel.MessageIndex) MessageShape {
	if s.mentionDetector()(item) {
		return MessageShapeMention
	}
	if s.replyToBotDetector()(ctx, item) {
		return MessageShapeReplyToBot
	}
	if isCommandSample(item) {
		return MessageShapeCommand
	}
	return MessageShapeAmbientGroupMessage
}

func (s SampleSelectorService) messageLoader() sampleMessageLoader {
	if s.loadMessages != nil {
		return s.loadMessages
	}
	return defaultSampleMessageLoader
}

func (s SampleSelectorService) mentionDetector() sampleMentionDetector {
	if s.isMention != nil {
		return s.isMention
	}
	return defaultSampleMentionDetector
}

func (s SampleSelectorService) replyToBotDetector() sampleReplyToBotDetector {
	if s.isReplyToBot != nil {
		return s.isReplyToBot
	}
	return defaultSampleReplyToBotDetector
}

func defaultSampleMessageLoader(ctx context.Context, options SampleFilterOptions) ([]*xmodel.MessageIndex, error) {
	options = normalizeSampleFilterOptions(options)
	windowStart := time.Now().AddDate(0, 0, -options.Days).Format(time.RFC3339)
	size := uint64(max(options.Limit*5, 100))
	return history.New(ctx).
		Query(osquery.Bool().Must(
			osquery.Term("chat_id", options.ChatID),
			osquery.Range("create_time_v2").Gte(windowStart),
		)).
		Source("message_id", "chat_id", "chat_name", "create_time", "create_time_v2", "raw_message", "message_str", "message_type", "mentions", "is_command", "main_command", "parent_id").
		Size(size).
		Sort("create_time_v2", osquery.OrderDesc).
		GetAll()
}

func normalizeSampleFilterOptions(options SampleFilterOptions) SampleFilterOptions {
	options.ChatID = strings.TrimSpace(options.ChatID)
	options.Keyword = strings.TrimSpace(options.Keyword)
	if options.Days <= 0 {
		options.Days = 7
	}
	if options.Limit <= 0 {
		options.Limit = 20
	}
	return options
}

func defaultSampleMentionDetector(item *xmodel.MessageIndex) bool {
	if item == nil {
		return false
	}
	return strings.Contains(strings.TrimSpace(item.RawMessage), "@bot")
}

func defaultSampleReplyToBotDetector(context.Context, *xmodel.MessageIndex) bool {
	return false
}

func isCommandSample(item *xmodel.MessageIndex) bool {
	if item == nil {
		return false
	}
	if item.IsCommand || strings.TrimSpace(item.MainCommand) != "" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(firstNonEmpty(item.RawMessage, item.Content)), "/")
}

func detectContentFeatures(text, messageType string) []string {
	text = strings.TrimSpace(text)
	messageType = strings.ToLower(strings.TrimSpace(messageType))
	features := make([]string, 0, 4)
	if looksLikeQuestion(text) {
		features = append(features, "question")
	}
	if len([]rune(text)) >= 80 {
		features = append(features, "long_message")
	}
	if strings.Contains(strings.ToLower(text), "http://") || strings.Contains(strings.ToLower(text), "https://") {
		features = append(features, "has_link")
	}
	switch messageType {
	case "image", "file", "media", "audio", "post", "interactive", "sticker":
		features = append(features, "has_attachment")
	}
	return features
}

func looksLikeQuestion(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	needles := []string{"?", "？", "吗", "么", "怎么", "为什么", "是否", "能不能", "如何", "怎么样"}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func matchesSampleFilters(sample ReplaySample, options SampleFilterOptions) bool {
	if options.ChatID != "" && sample.ChatID != options.ChatID {
		return false
	}
	if len(options.AllowedShapes) > 0 {
		matched := false
		for _, allowed := range options.AllowedShapes {
			if sample.PrimaryShape == allowed {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	text := strings.ToLower(strings.TrimSpace(sample.RawMessage))
	if options.Keyword != "" && !strings.Contains(text, strings.ToLower(options.Keyword)) {
		return false
	}
	featureSet := make(map[string]struct{}, len(sample.ContentFeatures))
	for _, feature := range sample.ContentFeatures {
		featureSet[feature] = struct{}{}
	}
	if options.RequireQuestion {
		if _, ok := featureSet["question"]; !ok {
			return false
		}
	}
	if options.RequireLongMessage {
		if _, ok := featureSet["long_message"]; !ok {
			return false
		}
	}
	if options.RequireLink {
		if _, ok := featureSet["has_link"]; !ok {
			return false
		}
	}
	if options.RequireAttachment {
		if _, ok := featureSet["has_attachment"]; !ok {
			return false
		}
	}
	return true
}
