package business

import (
	"context"
	"errors"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/accounts"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

type PersonalAccountStore interface {
	Resolve(context.Context, accounts.Principal) (string, error)
	Status(context.Context, accounts.Principal) (string, error)
	Invalidate(context.Context, accounts.Principal, string) error
	BeginBinding(context.Context, accounts.Principal, string) (accounts.Binding, error)
	EnsureBinding(context.Context, accounts.Principal, string) (accounts.Binding, error)
	SetBindingMessage(context.Context, accounts.Principal, string, string) error
}
type PrivateBindingSender interface {
	SendLuckinBinding(context.Context, domain.Scope, string, string) (string, error)
}

// ManagedLuckinAccounts keeps personal credentials outside model inputs and
// deployment configuration. Only the verified private card callback can write.
type ManagedLuckinAccounts struct {
	Store  PersonalAccountStore
	Sender PrivateBindingSender
}
type BindingReceipt struct {
	Outcome          string    `json:"outcome"`
	DeliveryLocation string    `json:"delivery_location,omitempty"`
	MessageID        string    `json:"message_id,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitzero"`
	AccountStatus    string    `json:"account_status"`
	Visibility       string    `json:"visibility"`
}

func (m *ManagedLuckinAccounts) Resolve(ctx context.Context, s domain.Scope, actor string) (Credential, error) {
	if m == nil || m.Store == nil || s.Validate() != nil || actor == "" {
		return Credential{}, domain.ErrUnauthorized
	}
	token, err := m.Store.Resolve(ctx, accounts.Principal{Scope: s, ActorID: actor})
	if err != nil {
		return Credential{}, err
	}
	return Credential{ActorID: actor, Token: token}, nil
}
func (m *ManagedLuckinAccounts) Invalidate(ctx context.Context, s domain.Scope, actor, token string) error {
	if m == nil || m.Store == nil {
		return domain.ErrUnauthorized
	}
	return m.Store.Invalidate(ctx, accounts.Principal{Scope: s, ActorID: actor}, token)
}
func (m *ManagedLuckinAccounts) RequestBinding(ctx context.Context, in Identity, manage bool) (BindingReceipt, error) {
	if !in.valid() || in.ExecutionKey == "" {
		return BindingReceipt{}, domain.ErrInvalid
	}
	if m == nil || m.Store == nil {
		return BindingReceipt{}, ErrProvider
	}
	p := accounts.Principal{Scope: in.Scope, ActorID: in.ActorID}
	status, err := m.Store.Status(ctx, p)
	if err != nil {
		return BindingReceipt{}, ErrProvider
	}
	receipt := BindingReceipt{AccountStatus: status, Visibility: "current_actor_only"}
	if !manage && status == accounts.StatusBound {
		receipt.Outcome = "already_bound"
		return receipt, nil
	}
	if m.Sender == nil {
		return BindingReceipt{}, ErrProvider
	}
	begin := m.Store.EnsureBinding
	if manage {
		begin = m.Store.BeginBinding
	}
	binding, err := begin(ctx, p, domain.StableID(in.Scope.Key(), in.TaskID, in.ExecutionKey))
	if err != nil {
		if errors.Is(err, domain.ErrEffectUnknown) {
			return BindingReceipt{}, domain.ErrEffectUnknown
		}
		return BindingReceipt{}, ErrProvider
	}
	message := binding.MessageID
	receipt.Outcome = "reused"
	if message == "" {
		receipt.Outcome = "sent"
		message, err = m.Sender.SendLuckinBinding(ctx, in.Scope, in.ActorID, binding.ID)
		if err != nil || message == "" {
			return BindingReceipt{}, domain.ErrEffectUnknown
		}
		if err = m.Store.SetBindingMessage(ctx, p, binding.ID, message); err != nil {
			return BindingReceipt{}, domain.ErrEffectUnknown
		}
	}
	receipt.MessageID, receipt.ExpiresAt = message, binding.ExpiresAt
	receipt.DeliveryLocation = "current_chat"
	return receipt, nil
}
