// Package mememodel adapts MASTER Ark transports to the independent meme stages.
package mememodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/llmusage"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

type Gate interface {
	Acquire(context.Context, string) (func(), error)
}
type Call func(context.Context, *responses.ResponsesRequest, llmusage.Scope) (*responses.ResponseObject, error)
type Options struct {
	ModelID         string
	Scope           llmusage.Scope
	Call            Call
	Gate            Gate
	MaxOutputTokens int64
}
type Model struct{ options Options }

var ErrTransport = errors.New("meme model transport failed")

func New(o Options) (*Model, error) {
	if strings.TrimSpace(o.ModelID) == "" {
		return nil, errors.New("meme model ID required")
	}
	if o.Call == nil {
		o.Call = ark_dal.CreateResponses
	}
	if o.MaxOutputTokens == 0 {
		o.MaxOutputTokens = 4096
	}
	if o.MaxOutputTokens < 1 || o.MaxOutputTokens > 16384 {
		return nil, errors.New("invalid meme token limit")
	}
	return &Model{options: o}, nil
}

func (m *Model) Complete(ctx context.Context, in memes.CompletionRequest) (memes.CompletionResponse, error) {
	var result memes.CompletionResponse
	if !json.Valid(in.InputJSON) || len(in.InputJSON) > 256<<10 || in.SystemPrompt == "" || len(in.SystemPrompt) > 128<<10 {
		return result, errors.New("invalid meme completion input")
	}
	switch in.Stage {
	case memes.StageQuery, memes.StageEncode:
	default:
		return result, errors.New("invalid meme completion stage")
	}
	system := in.SystemPrompt
	format := &responses.TextFormat{Type: responses.TextType_json_object}
	if len(in.Schema) > 0 {
		if !json.Valid(in.Schema) {
			return result, errors.New("invalid stage schema")
		}
		system += "\n本阶段输出 JSON Schema：\n" + string(in.Schema)
		strict := false
		format = &responses.TextFormat{Type: responses.TextType_json_schema, Schema: &responses.Bytes{Value: append([]byte(nil), in.Schema...)}, Name: "meme_" + string(in.Stage), Strict: &strict}
	}
	release := func() {}
	if m.options.Gate != nil {
		var err error
		release, err = m.options.Gate.Acquire(ctx, "model")
		if err != nil {
			return result, err
		}
	}
	defer release()
	input := []*responses.InputItem{}
	for _, message := range []struct {
		role responses.MessageRole_Enum
		text string
	}{{responses.MessageRole_system, system}, {responses.MessageRole_user, string(in.InputJSON)}} {
		input = append(input, &responses.InputItem{Union: &responses.InputItem_InputMessage{InputMessage: &responses.ItemInputMessage{Role: message.role, Content: []*responses.ContentItem{{Union: &responses.ContentItem_Text{Text: &responses.ContentItemText{Type: responses.ContentItemType_input_text, Text: message.text}}}}}}})
	}
	request := &responses.ResponsesRequest{Model: m.options.ModelID, Input: &responses.ResponsesInput{Union: &responses.ResponsesInput_ListValue{ListValue: &responses.InputItemList{ListValue: input}}}, Text: &responses.ResponsesText{Format: format}, Thinking: &responses.ResponsesThinking{Type: responses.ThinkingType_disabled.Enum()}, Reasoning: &responses.ResponsesReasoning{Effort: responses.ReasoningEffort_minimal}, MaxOutputTokens: &m.options.MaxOutputTokens}
	scope := m.options.Scope
	scope.Source = "meme_reply_" + string(in.Stage)
	scope.BusinessScene = llmusage.SceneConversation
	scope.BusinessOperation = llmusage.OperationChatReply
	started := time.Now()
	response, err := m.options.Call(ctx, request, scope)
	digest := sha256.Sum256([]byte(system))
	result.Metadata = memes.CallMetadata{Model: m.options.ModelID, PromptHash: hex.EncodeToString(digest[:]), DurationMS: time.Since(started).Milliseconds()}
	// Usage belongs to the completed provider call even when its generated output cannot be consumed.
	if usage := response.GetUsage(); usage != nil {
		result.Metadata.InputTokens = int(usage.GetInputTokens())
		result.Metadata.OutputTokens = int(usage.GetOutputTokens())
	}
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, safeTransportError(err)
	}
	if response == nil {
		return result, &TransportError{Code: "empty_response"}
	}
	if statusErr := responseStatusError(response); statusErr != nil {
		return result, statusErr
	}
	if usage := response.GetUsage(); usage != nil && usage.GetOutputTokens() > m.options.MaxOutputTokens {
		return result, &TransportError{HTTPStatus: 200, Code: "output_token_limit", Param: "max_output_tokens"}
	}
	var text strings.Builder
	for _, item := range response.GetOutput() {
		if message := item.GetOutputMessage(); message != nil {
			for _, content := range message.GetContent() {
				part := content.GetText().GetText()
				if len(part) > (256<<10)-text.Len() {
					return result, &TransportError{HTTPStatus: 200, Code: "output_size_limit", Param: "output"}
				}
				text.WriteString(part)
			}
		}
	}
	if text.Len() == 0 {
		return result, &TransportError{HTTPStatus: 200, Code: "empty_output", Param: "output"}
	}
	result.JSON = json.RawMessage(text.String())
	return result, nil
}

func responseStatusError(response *responses.ResponseObject) error {
	code := ""
	switch response.GetStatus() {
	case responses.ResponseStatus_completed:
		return nil
	case responses.ResponseStatus_incomplete:
		code = "response_incomplete"
	case responses.ResponseStatus_failed:
		code = "response_failed"
	case responses.ResponseStatus_in_progress:
		code = "response_in_progress"
	case responses.ResponseStatus_unspecified:
		code = "response_status_missing"
	default:
		code = "response_status_invalid"
	}
	param := ""
	if response.GetStatus() == responses.ResponseStatus_incomplete {
		switch reason := response.GetIncompleteDetails().GetReason(); reason {
		case "max_output_tokens", "max_tokens", "length", "content_filter", "max_context_tokens":
			param = reason
		}
	}
	return &TransportError{HTTPStatus: 200, Code: code, Param: param}
}

type EmbeddingCall func(context.Context, string, llmusage.Scope) ([]float32, arkmodel.Usage, error)
type Embedder struct {
	Scope       llmusage.Scope
	Call        EmbeddingCall
	Gate        Gate
	Concurrency int
}

func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > 256 {
		return nil, errors.New("invalid meme embedding batch")
	}
	concurrency := e.Concurrency
	if concurrency == 0 {
		concurrency = 1
	}
	if concurrency < 1 || concurrency > 64 {
		return nil, errors.New("invalid meme embedding concurrency")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len(text) > 32<<10 {
			return nil, errors.New("invalid meme embedding input")
		}
	}
	call := e.Call
	if call == nil {
		call = ark_dal.EmbeddingTextPrivate
	}
	scope := e.Scope
	scope.Source = "meme_reply_embedding"
	scope.BusinessScene = llmusage.SceneRetrieval
	scope.BusinessOperation = llmusage.OperationRetrieverEmbedding
	out := make([][]float32, len(texts))
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	fail := func(err error) {
		once.Do(func() {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			firstErr = err
			cancel()
		})
	}
	for range min(concurrency, len(texts)) {
		wg.Go(func() {
			for i := range jobs {
				if work.Err() != nil {
					return
				}
				release := func() {}
				if e.Gate != nil {
					var err error
					release, err = e.Gate.Acquire(work, "model")
					if err != nil {
						fail(err)
						return
					}
				}
				vector, _, err := call(work, texts[i], scope)
				release()
				if err != nil || len(vector) == 0 {
					fail(ErrTransport)
					return
				}
				out[i] = vector
			}
		})
	}
schedule:
	for i := range texts {
		select {
		case jobs <- i:
		case <-work.Done():
			break schedule
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, nil
}
