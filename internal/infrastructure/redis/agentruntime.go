package redis_dal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/redis/go-redis/v9"
)

type ResumeEvent struct {
	RunID       string          `json:"run_id"`
	StepID      string          `json:"step_id,omitempty"`
	Revision    int64           `json:"revision"`
	Source      string          `json:"source"`
	Token       string          `json:"token,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	PayloadJSON json.RawMessage `json:"payload_json,omitempty"`
	ActorOpenID string          `json:"actor_open_id,omitempty"`
	OccurredAt  time.Time       `json:"occurred_at,omitempty"`
}

type AgentRuntimeStore struct {
	client   *redis.Client
	identity botidentity.Identity
}

func NewAgentRuntimeStore(client *redis.Client, identity botidentity.Identity) *AgentRuntimeStore {
	return &AgentRuntimeStore{
		client:   client,
		identity: identity,
	}
}

func (s *AgentRuntimeStore) AcquireRunLock(ctx context.Context, runID, owner string, ttl time.Duration) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.client.SetNX(ctx, s.runLockKey(runID), strings.TrimSpace(owner), ttl).Result()
}

func (s *AgentRuntimeStore) ReleaseRunLock(ctx context.Context, runID, owner string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}

	key := s.runLockKey(runID)
	released := false
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		current, err := tx.Get(ctx, key).Result()
		if err == redis.Nil {
			return nil
		}
		if err != nil {
			return err
		}
		if current != strings.TrimSpace(owner) {
			return nil
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, key)
			return nil
		})
		if err != nil {
			return err
		}
		released = true
		return nil
	}, key)
	if err == redis.TxFailedErr {
		return false, nil
	}
	return released, err
}

func (s *AgentRuntimeStore) SwapActiveChatRun(ctx context.Context, chatID, expectedRunID, newRunID string, ttl time.Duration) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}

	key := s.activeChatRunKey(chatID)
	swapped := false
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		current, err := tx.Get(ctx, key).Result()
		if err == redis.Nil {
			current = ""
		} else if err != nil {
			return err
		}
		if current != strings.TrimSpace(expectedRunID) {
			return nil
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			if strings.TrimSpace(newRunID) == "" {
				pipe.Del(ctx, key)
				return nil
			}
			pipe.Set(ctx, key, strings.TrimSpace(newRunID), ttl)
			return nil
		})
		if err != nil {
			return err
		}
		swapped = true
		return nil
	}, key)
	if err == redis.TxFailedErr {
		return false, nil
	}
	return swapped, err
}

func (s *AgentRuntimeStore) ActiveChatRun(ctx context.Context, chatID string) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	result, err := s.client.Get(ctx, s.activeChatRunKey(chatID)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return result, err
}

func (s *AgentRuntimeStore) NextCancelGeneration(ctx context.Context, runID string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	return s.client.Incr(ctx, s.cancelGenerationKey(runID)).Result()
}

func (s *AgentRuntimeStore) EnqueueResumeEvent(ctx context.Context, event ResumeEvent) error {
	if err := s.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.client.RPush(ctx, s.resumeQueueKey(), payload).Err()
}

func (s *AgentRuntimeStore) DequeueResumeEvent(ctx context.Context, timeout time.Duration) (*ResumeEvent, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	result, err := s.client.BLPop(ctx, timeout, s.resumeQueueKey()).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(result) < 2 {
		return nil, nil
	}

	event := &ResumeEvent{}
	if err := json.Unmarshal([]byte(result[1]), event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *AgentRuntimeStore) validate() error {
	if s == nil || s.client == nil {
		return errors.New("agent runtime redis client is nil")
	}
	return nil
}

func (s *AgentRuntimeStore) runLockKey(runID string) string {
	return s.identity.NamespaceKey("agent_runtime", "run_lock", strings.TrimSpace(runID))
}

func (s *AgentRuntimeStore) activeChatRunKey(chatID string) string {
	return s.identity.NamespaceKey("agent_runtime", "active_chat_run", strings.TrimSpace(chatID))
}

func (s *AgentRuntimeStore) cancelGenerationKey(runID string) string {
	return s.identity.NamespaceKey("agent_runtime", "cancel_generation", strings.TrimSpace(runID))
}

func (s *AgentRuntimeStore) resumeQueueKey() string {
	return s.identity.NamespaceKey("agent_runtime", "resume_queue")
}
