package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/modelapi"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/pgstore"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/telemetry"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memereference"
	memeknowledge "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
)

type referenceUtterance struct {
	turn memeknowledge.Turn
	at   time.Time
	refs []domain.SourceRef
}

// Inputs come from durable raw messages and confirmed delivery receipts, before
// the presenter substitutes names or attaches derived media. Preserve the whole
// current utterance, then causal bound context, then the most recent dialogue.
// An oversized current utterance skips retrieval; clipping could reverse intent.
func memeReferenceDialogue(current domain.Message, bound, recent []domain.Message, replies []pgstore.ConversationReply, botID string) ([]memeknowledge.Turn, []domain.SourceRef, int) {
	if current.Source.MessageID == "" || strings.TrimSpace(current.Text) == "" || utf8.RuneCountInString(current.Text) > memeknowledge.MaxInputRunes || len(current.Text) > memeknowledge.MaxInputBytes {
		return nil, nil, 1
	}
	selected := []referenceUtterance{}
	seen := map[string]bool{}
	runes, bytes, omitted := 0, 0, 0
	add := func(item referenceUtterance) {
		if seen[item.turn.TurnID] {
			return
		}
		seen[item.turn.TurnID] = true
		if item.at.After(current.Source.OccurredAt) || item.turn.TurnID == "" || item.turn.Speaker == "" || strings.TrimSpace(item.turn.Text) == "" {
			return
		}
		n := utf8.RuneCountInString(item.turn.Text)
		if len(selected) >= memeknowledge.MaxMessages || runes+n > memeknowledge.MaxInputRunes || bytes+len(item.turn.Text) > memeknowledge.MaxInputBytes {
			omitted++
			return
		}
		selected = append(selected, item)
		runes += n
		bytes += len(item.turn.Text)
	}
	message := func(m domain.Message) {
		var meta domain.IngressMetadata
		if m.Deleted || strings.HasPrefix(m.Source.MessageID, "card:") || (len(m.Raw) > 0 && json.Unmarshal(m.Raw, &meta) != nil) {
			return
		}
		if meta.SenderType != "" && meta.SenderType != "user" || meta.Kind != "" && meta.Kind != "message" && meta.Kind != "refresh" {
			return
		}
		add(referenceUtterance{turn: memeknowledge.Turn{TurnID: m.Source.MessageID, Speaker: m.Source.AuthorID, Text: m.Text}, at: m.Source.OccurredAt, refs: []domain.SourceRef{m.Source}})
	}
	message(current)
	if len(selected) == 0 {
		return nil, nil, 1
	}
	// Bound material can carry a decisive antecedent outside the recent window.
	for _, m := range bound {
		message(m)
	}
	history := make([]referenceUtterance, 0, len(recent)+len(replies))
	for _, m := range recent {
		var meta domain.IngressMetadata
		if m.Deleted || strings.HasPrefix(m.Source.MessageID, "card:") || (len(m.Raw) > 0 && json.Unmarshal(m.Raw, &meta) != nil) {
			continue
		}
		if meta.SenderType != "" && meta.SenderType != "user" || meta.Kind != "" && meta.Kind != "message" && meta.Kind != "refresh" {
			continue
		}
		history = append(history, referenceUtterance{turn: memeknowledge.Turn{TurnID: m.Source.MessageID, Speaker: m.Source.AuthorID, Text: m.Text}, at: m.Source.OccurredAt, refs: []domain.SourceRef{m.Source}})
	}
	for _, reply := range replies {
		history = append(history, referenceUtterance{turn: memeknowledge.Turn{TurnID: reply.MessageID, Speaker: botID, Text: reply.Text}, at: reply.OccurredAt, refs: reply.Sources})
	}
	sort.SliceStable(history, func(i, j int) bool {
		if history[i].at.Equal(history[j].at) {
			return history[i].turn.TurnID > history[j].turn.TurnID
		}
		return history[i].at.After(history[j].at)
	})
	for _, item := range history {
		add(item)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].at.Equal(selected[j].at) {
			return selected[i].turn.TurnID < selected[j].turn.TurnID
		}
		return selected[i].at.Before(selected[j].at)
	})
	turns := make([]memeknowledge.Turn, 0, len(selected))
	refs := []domain.SourceRef{}
	for _, item := range selected {
		turns = append(turns, item.turn)
		refs = append(refs, item.refs...)
	}
	return turns, sceneReferences(refs, nil, nil), omitted
}

func memeReferenceCards(result memeknowledge.ReferenceResult) []memeknowledge.UseCard {
	// Reuse the shared formatter's ready-status, uniqueness and five-card gate.
	block := memereference.BuildBlock(result)
	start := strings.Index(block, "[")
	if start < 0 {
		return nil
	}
	var cards []memeknowledge.UseCard
	if json.Unmarshal([]byte(block[start:]), &cards) != nil {
		return nil
	}
	return cards
}

// Each Execute gets its own tracker. Its only input is the actual model-bound
// runtime scene, including a scene restored by Eino from an approval checkpoint.
// No retrieval, provider generation read or additional model call happens here.
type memeReferenceChat struct {
	client           Chat
	currentMessageID string
	currentChatID    string
	cards            []memeknowledge.UseCard
	revalidate       memereference.Revalidator
	knowledgeIDs     []string
}

func (m *memeReferenceChat) Generate(ctx context.Context, request modelapi.ChatRequest) (modelapi.ChatResponse, error) {
	m.cards = nil
	m.knowledgeIDs = nil
	hasKnowledge := false
	for index, message := range request.Messages {
		if message.Role != "user" {
			continue
		}
		text, ok := message.Content.(string)
		if !ok {
			continue
		}
		var scene struct {
			CurrentUtterance struct{ MessageID string }
			MemeReferences   []memeknowledge.UseCard
			MemeKnowledge    *memeknowledge.GroundedContext
		}
		// Parse the top-level runtime scene once. Never interpret human Text,
		// tool observations or assistant output as supplied reference metadata.
		if json.Unmarshal([]byte(text), &scene) != nil || scene.CurrentUtterance.MessageID == "" || scene.CurrentUtterance.MessageID != m.currentMessageID {
			continue
		}
		originalContext := scene.MemeKnowledge
		originalCards := scene.MemeReferences
		if memereference.PromptContext(memeknowledge.ReferenceResult{ContextV2: originalContext}) != nil {
			m.knowledgeIDs = append(m.knowledgeIDs, memereference.GroundedIdentifiers(originalContext)...)
		}
		if m.revalidate != nil {
			clean, cards, err := m.revalidate(ctx, m.currentChatID, scene.MemeKnowledge, scene.MemeReferences)
			if err != nil {
				clean = nil
				cards = nil
				telemetry.Info(ctx, "agent.memes.environment.revalidated", "status", "unavailable")
			}
			scene.MemeKnowledge = memereference.PromptContext(memeknowledge.ReferenceResult{ContextV2: clean})
			scene.MemeReferences = cards
			if !reflect.DeepEqual(originalContext, scene.MemeKnowledge) || !reflect.DeepEqual(originalCards, cards) {
				var object map[string]json.RawMessage
				if json.Unmarshal([]byte(text), &object) != nil {
					return modelapi.ChatResponse{}, domain.ErrInvalid
				}
				if scene.MemeKnowledge == nil {
					delete(object, "MemeKnowledge")
				} else {
					object["MemeKnowledge"], _ = json.Marshal(scene.MemeKnowledge)
				}
				if len(cards) == 0 {
					delete(object, "MemeReferences")
				} else {
					object["MemeReferences"], _ = json.Marshal(cards)
				}
				encoded, err := json.Marshal(object)
				if err != nil {
					return modelapi.ChatResponse{}, domain.ErrInvalid
				}
				request.Messages = append([]modelapi.ChatMessage(nil), request.Messages...)
				request.Messages[index].Content = string(encoded)
			}
		}
		m.cards = memeReferenceCards(memeknowledge.ReferenceResult{Status: memeknowledge.ReferenceStatusReady, Cards: scene.MemeReferences})
		hasKnowledge = memereference.PromptContext(memeknowledge.ReferenceResult{ContextV2: scene.MemeKnowledge}) != nil
	}
	if !hasKnowledge {
		for i, message := range request.Messages {
			if message.Role != "system" {
				continue
			}
			if text, ok := message.Content.(string); ok && strings.Contains(text, memereference.KnowledgeInstruction) {
				request.Messages = append([]modelapi.ChatMessage(nil), request.Messages...)
				request.Messages[i].Content = strings.ReplaceAll(text, memereference.KnowledgeInstruction, "")
			}
		}
	}
	if hasKnowledge {
		request.Messages = append([]modelapi.ChatMessage(nil), request.Messages...)
		for i, message := range request.Messages {
			if message.Role != "system" {
				continue
			}
			if text, ok := message.Content.(string); ok && !strings.Contains(text, memereference.KnowledgeInstruction) {
				request.Messages[i].Content = text + "\n\n" + memereference.KnowledgeInstruction
			}
		}
	}
	return m.client.Generate(ctx, request)
}

func (m *memeReferenceChat) safeKnowledgePlan(plan domain.ConversationPlan) bool {
	if memereference.ContainsSuppliedIdentifier(plan.Summary, m.knowledgeIDs) {
		return false
	}
	for _, message := range plan.Messages {
		if memereference.ContainsSuppliedIdentifier(message.Text, m.knowledgeIDs) {
			return false
		}
	}
	return true
}

func (m *memeReferenceChat) ModelIdentity() (string, string) {
	if identified, ok := m.client.(interface{ ModelIdentity() (string, string) }); ok {
		return identified.ModelIdentity()
	}
	return "", ""
}

func (m *memeReferenceChat) suppliedIDs() []string {
	ids := make([]string, 0, len(m.cards))
	for _, card := range m.cards {
		ids = append(ids, card.UseID)
	}
	return ids
}

var memeReferenceJSONKey = regexp.MustCompile(`(?i)["'](?:use_id|family_id|input_relation|review_status|applicability_notes|relation_kinds)["']\s*:`)
var memeReferenceListLine = regexp.MustCompile(`^(?:[-*•]\s+|(?:[0-9]+|[一二三四五六七八九十]+)[.)、．]\s*)`)

// This output boundary complements the model instruction. The runtime never
// freezes reference lists, card JSON or retrieval/selection narration as public
// answer content. Ordinary uses of "候选" and recognizable meme phrasing remain
// valid: textual list/narration checks are tied to cards actually supplied.
func safeMemeReferenceVisibleText(text string, cards []memeknowledge.UseCard) bool {
	for _, token := range []string{"meme_use_ids", "MemeKnowledge", `"prior_knowledge"`, `"entry_fingerprint"`, `"anchor_ids"`, `"knowledge_ids"`, "=== 梗背景与语境参考 ===", "MemeReferences", "retrieved_use_ids", "selected_use_ids", "=== 重要梗用法参考 ==="} {
		if strings.Contains(text, token) {
			return false
		}
	}
	if memeReferenceJSONKey.MatchString(text) {
		return false
	}
	for _, card := range cards {
		if card.UseID != "" && strings.Contains(text, card.UseID) {
			// Opaque catalog identifiers are internal metadata. Avoid treating a
			// short ordinary word/number used as a fixture ID as a global ban.
			if strings.TrimSpace(text) == card.UseID || strings.ContainsAny(card.UseID, ":./_-") || len(card.UseID) > 16 {
				return false
			}
			for _, narration := range []string{"选中", "选用", "选择的 ID", "使用的 ID"} {
				if strings.Contains(text, narration) {
					return false
				}
			}
		}
		if card.Name == "" {
			continue
		}
		if strings.Contains(text, card.Name) {
			for _, heading := range []string{"候选列表", "候选卡片", "检索过程", "选择过程", "选用过程", "检索结果", "召回结果"} {
				if strings.Contains(text, heading) {
					return false
				}
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if !strings.Contains(line, card.Name) {
				continue
			}
			if memeReferenceListLine.MatchString(strings.TrimSpace(line)) {
				return false
			}
			for _, narration := range []string{"候选", "检索", "召回", "排名", "我选择", "我选了", "我选用"} {
				if strings.Contains(line, narration) {
					return false
				}
			}
		}
	}
	return true
}

func safeMemeReferenceVisiblePlan(plan domain.ConversationPlan, cards []memeknowledge.UseCard) bool {
	visible := []string{plan.Summary}
	if !safeMemeReferenceVisibleText(plan.Summary, cards) {
		return false
	}
	for _, message := range plan.Messages {
		visible = append(visible, message.Text)
		if !safeMemeReferenceVisibleText(message.Text, cards) {
			return false
		}
	}
	// Headers and their candidate entries can be split between the summary and
	// later sections. Validate the assembled public content before any freeze.
	return safeMemeReferenceVisibleText(strings.Join(visible, "\n"), cards)
}

func logMemeRetrieval(ctx context.Context, result memeknowledge.ReferenceResult, cards []memeknowledge.UseCard) {
	ids, names, channels := []string{}, []string{}, []string{}
	for _, card := range cards {
		ids = append(ids, card.UseID)
		names = append(names, card.Name)
	}
	queries := result.Retrieval.RawQueries
	if queries == nil {
		queries = result.Retrieval.Queries
	}
	seen := map[string]bool{}
	for _, query := range queries {
		for _, channel := range query.Channels {
			label := string(query.Query.Field) + ":" + channel.Channel + ":" + channel.Status
			if !seen[label] {
				channels = append(channels, label)
				seen[label] = true
			}
		}
	}
	telemetry.Info(ctx, "agent.turn.memes.retrieved", "grounded", memereference.GroundedDiagnostics(result), "reference_status", string(result.Status), "retrieved_use_ids", ids, "retrieved_names", names, "retrieval_channels", channels, "elapsed_ms", result.Metadata.ElapsedMS, "semantic_calls", result.Metadata.SemanticCalls, "repair_calls", result.Metadata.RepairCalls)
}

func logMemeAttribution(ctx context.Context, plan domain.ConversationPlan, supplied []string) {
	allowed, seen := map[string]bool{}, map[string]bool{}
	for _, id := range supplied {
		allowed[id] = true
	}
	selected, invalid := []string{}, []string{}
	status := "unreported"
	if plan.MemeUseIDs != nil {
		status = "unused"
	}
	for _, id := range plan.MemeUseIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if allowed[id] {
			selected = append(selected, id)
		} else {
			invalid = append(invalid, id)
		}
	}
	if len(selected) > 0 {
		status = "reported"
	}
	if plan.MemeUseIDsMalformed || len(invalid) > 0 {
		status = "invalid"
	}
	telemetry.Info(ctx, "agent.turn.memes.attribution", "attribution_status", status, "selected_use_ids", selected, "invalid_use_ids", invalid, "supplied_count", len(supplied), "model_reported", plan.MemeUseIDs != nil, "malformed_report", plan.MemeUseIDsMalformed)
}
