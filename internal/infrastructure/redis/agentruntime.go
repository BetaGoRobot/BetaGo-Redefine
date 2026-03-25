package redis_dal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/botidentity"
	"github.com/redis/go-redis/v9"
)

var ErrPendingInitialRunQueueFull = errors.New("agent runtime pending initial run queue full")

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

type PendingInitialScope struct {
	ChatID      string `json:"chat_id,omitempty"`
	ActorOpenID string `json:"actor_open_id,omitempty"`
}

type AgentRuntimeStore struct {
	client   *redis.Client
	identity botidentity.Identity
}

const pendingInitialScopeMemberSeparator = "\x1f"

var acquireExecutionLeaseScript = redis.NewScript(`
local time = redis.call("TIME")
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now)
if redis.call("ZSCORE", KEYS[1], ARGV[1]) then
  redis.call("ZADD", KEYS[1], ARGV[2], ARGV[1])
  redis.call("PEXPIRE", KEYS[1], ARGV[3])
  return 1
end
local count = redis.call("ZCARD", KEYS[1])
if tonumber(ARGV[4]) > 0 and count >= tonumber(ARGV[4]) then
  return 0
end
redis.call("ZADD", KEYS[1], ARGV[2], ARGV[1])
redis.call("PEXPIRE", KEYS[1], ARGV[3])
return 1
`)

var renewExecutionLeaseScript = redis.NewScript(`
local time = redis.call("TIME")
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now)
if not redis.call("ZSCORE", KEYS[1], ARGV[1]) then
  return 0
end
redis.call("ZADD", KEYS[1], ARGV[2], ARGV[1])
redis.call("PEXPIRE", KEYS[1], ARGV[3])
return 1
`)

var releaseExecutionLeaseScript = redis.NewScript(`
local removed = redis.call("ZREM", KEYS[1], ARGV[1])
if redis.call("ZCARD", KEYS[1]) == 0 then
  redis.call("DEL", KEYS[1])
end
return removed
`)

var countExecutionLeaseScript = redis.NewScript(`
local time = redis.call("TIME")
local now = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000)
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now)
return redis.call("ZCARD", KEYS[1])
`)

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

func (s *AgentRuntimeStore) SwapActiveActorChatRun(ctx context.Context, chatID, actorOpenID, expectedRunID, newRunID string, ttl time.Duration) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}

	key := s.activeActorChatRunKey(chatID, actorOpenID)
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

func (s *AgentRuntimeStore) ActiveActorChatRun(ctx context.Context, chatID, actorOpenID string) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	result, err := s.client.Get(ctx, s.activeActorChatRunKey(chatID, actorOpenID)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return result, err
}

func (s *AgentRuntimeStore) AcquireExecutionLease(ctx context.Context, chatID, actorOpenID, holder string, ttl time.Duration, maxConcurrent int64) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	key := s.executionLeaseKey(chatID, actorOpenID)
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false, fmt.Errorf("execution lease holder is required")
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	expireAtMillis := time.Now().UTC().UnixMilli() + ttl.Milliseconds()
	retentionMillis := ttl.Milliseconds() * 3
	if retentionMillis < ttl.Milliseconds()+1000 {
		retentionMillis = ttl.Milliseconds() + 1000
	}
	result, err := acquireExecutionLeaseScript.Run(ctx, s.client, []string{key},
		holder,
		strconv.FormatInt(expireAtMillis, 10),
		strconv.FormatInt(retentionMillis, 10),
		strconv.FormatInt(maxConcurrent, 10),
	).Int64()
	return result == 1, err
}

func (s *AgentRuntimeStore) RenewExecutionLease(ctx context.Context, chatID, actorOpenID, holder string, ttl time.Duration) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	key := s.executionLeaseKey(chatID, actorOpenID)
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false, fmt.Errorf("execution lease holder is required")
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	expireAtMillis := time.Now().UTC().UnixMilli() + ttl.Milliseconds()
	retentionMillis := ttl.Milliseconds() * 3
	if retentionMillis < ttl.Milliseconds()+1000 {
		retentionMillis = ttl.Milliseconds() + 1000
	}
	result, err := renewExecutionLeaseScript.Run(ctx, s.client, []string{key},
		holder,
		strconv.FormatInt(expireAtMillis, 10),
		strconv.FormatInt(retentionMillis, 10),
	).Int64()
	return result == 1, err
}

func (s *AgentRuntimeStore) ReleaseExecutionLease(ctx context.Context, chatID, actorOpenID, holder string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	key := s.executionLeaseKey(chatID, actorOpenID)
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return false, fmt.Errorf("execution lease holder is required")
	}
	result, err := releaseExecutionLeaseScript.Run(ctx, s.client, []string{key}, holder).Int64()
	return result > 0, err
}

func (s *AgentRuntimeStore) ExecutionLeaseCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	key := s.executionLeaseKey(chatID, actorOpenID)
	result, err := countExecutionLeaseScript.Run(ctx, s.client, []string{key}).Int64()
	if err == redis.Nil {
		return 0, nil
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

func (s *AgentRuntimeStore) SaveApprovalReservation(ctx context.Context, stepID, token string, payload []byte, ttl time.Duration) error {
	if err := s.validate(); err != nil {
		return err
	}
	stepID = strings.TrimSpace(stepID)
	token = strings.TrimSpace(token)
	if stepID == "" {
		return fmt.Errorf("approval reservation step_id is required")
	}
	if token == "" {
		return fmt.Errorf("approval reservation token is required")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, s.approvalReservationKey(stepID), payload, ttl)
		pipe.Set(ctx, s.approvalReservationTokenKey(token), stepID, ttl)
		return nil
	})
	return err
}

func (s *AgentRuntimeStore) EnqueuePendingInitialRun(ctx context.Context, chatID, actorOpenID string, payload []byte, maxPending int64) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	key := s.pendingInitialRunListKey(chatID, actorOpenID)
	member := s.pendingInitialScopeMember(chatID, actorOpenID)

	for attempts := 0; attempts < 5; attempts++ {
		var position int64
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			count, err := tx.LLen(ctx, key).Result()
			if err != nil && err != redis.Nil {
				return err
			}
			if maxPending > 0 && count >= maxPending {
				return ErrPendingInitialRunQueueFull
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.RPush(ctx, key, payload)
				pipe.SAdd(ctx, s.pendingInitialScopeIndexKey(), member)
				return nil
			})
			if err != nil {
				return err
			}
			position = count + 1
			return nil
		}, key)
		if err == redis.TxFailedErr {
			continue
		}
		return position, err
	}
	return 0, redis.TxFailedErr
}

func (s *AgentRuntimeStore) PrependPendingInitialRun(ctx context.Context, chatID, actorOpenID string, payload []byte) error {
	if err := s.validate(); err != nil {
		return err
	}
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.LPush(ctx, s.pendingInitialRunListKey(chatID, actorOpenID), payload)
		pipe.SAdd(ctx, s.pendingInitialScopeIndexKey(), s.pendingInitialScopeMember(chatID, actorOpenID))
		return nil
	})
	return err
}

func (s *AgentRuntimeStore) ConsumePendingInitialRun(ctx context.Context, chatID, actorOpenID string) ([]byte, int64, error) {
	if err := s.validate(); err != nil {
		return nil, 0, err
	}
	key := s.pendingInitialRunListKey(chatID, actorOpenID)

	for attempts := 0; attempts < 5; attempts++ {
		var payload []byte
		var remaining int64
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.LIndex(ctx, key, 0).Bytes()
			if err == redis.Nil {
				payload = nil
				remaining = 0
				return nil
			}
			if err != nil {
				return err
			}

			lenCmd := tx.LLen(ctx, key)
			if _, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.LPop(ctx, key)
				return nil
			}); err != nil {
				return err
			}
			count, err := lenCmd.Result()
			if err != nil && err != redis.Nil {
				return err
			}
			payload = append([]byte(nil), raw...)
			if count > 0 {
				remaining = count - 1
			}
			return nil
		}, key)
		if err == redis.TxFailedErr {
			continue
		}
		return payload, remaining, err
	}
	return nil, 0, redis.TxFailedErr
}

func (s *AgentRuntimeStore) NotifyPendingInitialRun(ctx context.Context, chatID, actorOpenID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(PendingInitialScope{
		ChatID:      strings.TrimSpace(chatID),
		ActorOpenID: strings.TrimSpace(actorOpenID),
	})
	if err != nil {
		return err
	}
	return s.client.RPush(ctx, s.pendingInitialScopeQueueKey(), payload).Err()
}

func (s *AgentRuntimeStore) MarkPendingInitialScope(ctx context.Context, chatID, actorOpenID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	return s.client.SAdd(ctx, s.pendingInitialScopeIndexKey(), s.pendingInitialScopeMember(chatID, actorOpenID)).Err()
}

func (s *AgentRuntimeStore) ClearPendingInitialScopeIfEmpty(ctx context.Context, chatID, actorOpenID string) error {
	if err := s.validate(); err != nil {
		return err
	}
	listKey := s.pendingInitialRunListKey(chatID, actorOpenID)
	indexKey := s.pendingInitialScopeIndexKey()
	member := s.pendingInitialScopeMember(chatID, actorOpenID)

	for attempts := 0; attempts < 5; attempts++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			count, err := tx.LLen(ctx, listKey).Result()
			if err != nil && err != redis.Nil {
				return err
			}
			if count > 0 {
				return nil
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.SRem(ctx, indexKey, member)
				return nil
			})
			return err
		}, listKey, indexKey)
		if err == redis.TxFailedErr {
			continue
		}
		return err
	}
	return redis.TxFailedErr
}

func (s *AgentRuntimeStore) PendingInitialRunCount(ctx context.Context, chatID, actorOpenID string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	count, err := s.client.LLen(ctx, s.pendingInitialRunListKey(chatID, actorOpenID)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (s *AgentRuntimeStore) PendingInitialScopeCount(ctx context.Context) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	count, err := s.client.SCard(ctx, s.pendingInitialScopeIndexKey()).Result()
	if err == redis.Nil {
		return 0, nil
	}
	return count, err
}

func (s *AgentRuntimeStore) ListPendingInitialScopes(ctx context.Context, cursor uint64, count int64) ([]PendingInitialScope, uint64, error) {
	if err := s.validate(); err != nil {
		return nil, 0, err
	}
	if count <= 0 {
		count = 100
	}
	members, nextCursor, err := s.client.SScan(ctx, s.pendingInitialScopeIndexKey(), cursor, "", count).Result()
	if err == redis.Nil {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	scopes := make([]PendingInitialScope, 0, len(members))
	for _, member := range members {
		scope := parsePendingInitialScopeMember(member)
		if scope.ChatID == "" || scope.ActorOpenID == "" {
			continue
		}
		scopes = append(scopes, scope)
	}
	return scopes, nextCursor, nil
}

func (s *AgentRuntimeStore) DequeuePendingInitialScope(ctx context.Context, timeout time.Duration) (string, string, error) {
	if err := s.validate(); err != nil {
		return "", "", err
	}
	result, err := s.client.BLPop(ctx, timeout, s.pendingInitialScopeQueueKey()).Result()
	if err == redis.Nil {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if len(result) < 2 {
		return "", "", nil
	}

	scope := PendingInitialScope{}
	if err := json.Unmarshal([]byte(result[1]), &scope); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(scope.ChatID), strings.TrimSpace(scope.ActorOpenID), nil
}

func (s *AgentRuntimeStore) AcquirePendingInitialScopeLock(ctx context.Context, chatID, actorOpenID, owner string, ttl time.Duration) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.client.SetNX(ctx, s.pendingInitialScopeLockKey(chatID, actorOpenID), strings.TrimSpace(owner), ttl).Result()
}

func (s *AgentRuntimeStore) ReleasePendingInitialScopeLock(ctx context.Context, chatID, actorOpenID, owner string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	return s.releaseOwnedLock(ctx, s.pendingInitialScopeLockKey(chatID, actorOpenID), owner)
}

func (s *AgentRuntimeStore) LoadApprovalReservation(ctx context.Context, stepID, token string) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	resolvedStepID, err := s.resolveApprovalReservationStepID(ctx, stepID, token)
	if err != nil || resolvedStepID == "" {
		return nil, err
	}
	raw, err := s.client.Get(ctx, s.approvalReservationKey(resolvedStepID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

func (s *AgentRuntimeStore) RecordApprovalReservationDecision(ctx context.Context, stepID, token string, decisionPayload []byte) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	resolvedStepID, err := s.resolveApprovalReservationStepID(ctx, stepID, token)
	if err != nil || resolvedStepID == "" {
		return nil, err
	}
	stepKey := s.approvalReservationKey(resolvedStepID)
	tokenKey := s.approvalReservationTokenKey(strings.TrimSpace(token))
	if strings.TrimSpace(token) == "" {
		tokenKey = ""
	}

	var updated []byte
	for attempts := 0; attempts < 5; attempts++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, stepKey).Bytes()
			if err == redis.Nil {
				updated = nil
				return nil
			}
			if err != nil {
				return err
			}

			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(raw, &envelope); err != nil {
				return err
			}
			if existing := strings.TrimSpace(string(envelope["decision"])); existing != "" && existing != "null" {
				updated = append([]byte(nil), raw...)
				return nil
			}
			envelope["decision"] = append([]byte(nil), decisionPayload...)
			nextPayload, err := json.Marshal(envelope)
			if err != nil {
				return err
			}

			ttl, err := tx.PTTL(ctx, stepKey).Result()
			if err != nil && err != redis.Nil {
				return err
			}
			if ttl <= 0 {
				ttl = time.Hour
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, stepKey, nextPayload, ttl)
				if tokenKey != "" {
					pipe.Set(ctx, tokenKey, resolvedStepID, ttl)
				}
				return nil
			})
			if err != nil {
				return err
			}
			updated = append([]byte(nil), nextPayload...)
			return nil
		}, stepKey)
		if err == redis.TxFailedErr {
			continue
		}
		return updated, err
	}
	return nil, redis.TxFailedErr
}

func (s *AgentRuntimeStore) ConsumeApprovalReservation(ctx context.Context, stepID, token string) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	resolvedStepID, err := s.resolveApprovalReservationStepID(ctx, stepID, token)
	if err != nil || resolvedStepID == "" {
		return nil, err
	}
	stepKey := s.approvalReservationKey(resolvedStepID)

	var consumed []byte
	for attempts := 0; attempts < 5; attempts++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, stepKey).Bytes()
			if err == redis.Nil {
				consumed = nil
				return nil
			}
			if err != nil {
				return err
			}

			reservationToken := strings.TrimSpace(token)
			if reservationToken == "" {
				reservationToken = extractApprovalReservationToken(raw)
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, stepKey)
				if reservationToken != "" {
					pipe.Del(ctx, s.approvalReservationTokenKey(reservationToken))
				}
				return nil
			})
			if err != nil {
				return err
			}
			consumed = append([]byte(nil), raw...)
			return nil
		}, stepKey)
		if err == redis.TxFailedErr {
			continue
		}
		return consumed, err
	}
	return nil, redis.TxFailedErr
}

func (s *AgentRuntimeStore) validate() error {
	if s == nil || s.client == nil {
		return errors.New("agent runtime redis client is nil")
	}
	return nil
}

func (s *AgentRuntimeStore) releaseOwnedLock(ctx context.Context, key, owner string) (bool, error) {
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

func (s *AgentRuntimeStore) runLockKey(runID string) string {
	return s.identity.NamespaceKey("agent_runtime", "run_lock", strings.TrimSpace(runID))
}

func (s *AgentRuntimeStore) activeChatRunKey(chatID string) string {
	return s.identity.NamespaceKey("agent_runtime", "active_chat_run", strings.TrimSpace(chatID))
}

func (s *AgentRuntimeStore) activeActorChatRunKey(chatID, actorOpenID string) string {
	return s.identity.NamespaceKey("agent_runtime", "active_actor_chat_run", strings.TrimSpace(chatID), strings.TrimSpace(actorOpenID))
}

func (s *AgentRuntimeStore) executionLeaseKey(chatID, actorOpenID string) string {
	return s.identity.NamespaceKey("agent_runtime", "execution_lease", strings.TrimSpace(chatID), strings.TrimSpace(actorOpenID))
}

func (s *AgentRuntimeStore) cancelGenerationKey(runID string) string {
	return s.identity.NamespaceKey("agent_runtime", "cancel_generation", strings.TrimSpace(runID))
}

func (s *AgentRuntimeStore) resumeQueueKey() string {
	return s.identity.NamespaceKey("agent_runtime", "resume_queue")
}

func (s *AgentRuntimeStore) pendingInitialScopeQueueKey() string {
	return s.identity.NamespaceKey("agent_runtime", "pending_initial_scope_queue")
}

func (s *AgentRuntimeStore) pendingInitialScopeIndexKey() string {
	return s.identity.NamespaceKey("agent_runtime", "pending_initial_scope_index")
}

func (s *AgentRuntimeStore) pendingInitialRunListKey(chatID, actorOpenID string) string {
	return s.identity.NamespaceKey("agent_runtime", "pending_initial_run_list", strings.TrimSpace(chatID), strings.TrimSpace(actorOpenID))
}

func (s *AgentRuntimeStore) pendingInitialScopeLockKey(chatID, actorOpenID string) string {
	return s.identity.NamespaceKey("agent_runtime", "pending_initial_scope_lock", strings.TrimSpace(chatID), strings.TrimSpace(actorOpenID))
}

func (s *AgentRuntimeStore) approvalReservationKey(stepID string) string {
	return s.identity.NamespaceKey("agent_runtime", "approval_reservation", strings.TrimSpace(stepID))
}

func (s *AgentRuntimeStore) approvalReservationTokenKey(token string) string {
	return s.identity.NamespaceKey("agent_runtime", "approval_reservation_token", strings.TrimSpace(token))
}

func (s *AgentRuntimeStore) resolveApprovalReservationStepID(ctx context.Context, stepID, token string) (string, error) {
	if trimmed := strings.TrimSpace(stepID); trimmed != "" {
		return trimmed, nil
	}
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" {
		return "", nil
	}
	resolved, err := s.client.Get(ctx, s.approvalReservationTokenKey(trimmedToken)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func extractApprovalReservationToken(raw []byte) string {
	envelope := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	tokenRaw, ok := envelope["token"]
	if !ok {
		return ""
	}
	var token string
	if err := json.Unmarshal(tokenRaw, &token); err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func (s *AgentRuntimeStore) pendingInitialScopeMember(chatID, actorOpenID string) string {
	return strings.TrimSpace(chatID) + pendingInitialScopeMemberSeparator + strings.TrimSpace(actorOpenID)
}

func parsePendingInitialScopeMember(member string) PendingInitialScope {
	chatID, actorOpenID, ok := strings.Cut(strings.TrimSpace(member), pendingInitialScopeMemberSeparator)
	if !ok {
		return PendingInitialScope{}
	}
	return PendingInitialScope{
		ChatID:      strings.TrimSpace(chatID),
		ActorOpenID: strings.TrimSpace(actorOpenID),
	}
}
