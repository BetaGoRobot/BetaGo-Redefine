package luckin

import (
	"context"
	"errors"
	"strings"
)

const ProviderLuckin = "luckin"

type ScopeType string

const (
	ScopePersonal ScopeType = "personal"
	ScopeChat     ScopeType = "chat"
	ScopeSystem   ScopeType = "system"
)

type ChatType string

const (
	ChatTypePrivate ChatType = "private"
	ChatTypeGroup   ChatType = "group"
)

var ErrCredentialNotFound = errors.New("luckin credential not found")

type CredentialScope struct {
	Type ScopeType
	ID   string
}

type CredentialLookup struct {
	Provider  string
	AppID     string
	BotOpenID string
	Scope     CredentialScope
}

type Credential struct {
	Provider  string
	Scope     CredentialScope
	Token     string
	TokenHint string
}

type CredentialRequest struct {
	AppID     string
	BotOpenID string
	ChatID    string
	OpenID    string
	ChatType  ChatType
}

type CredentialStore interface {
	FindToken(context.Context, CredentialLookup) (Credential, error)
}

type CredentialResolver struct {
	store       CredentialStore
	systemToken string
}

func NewCredentialResolver(store CredentialStore, systemToken string) CredentialResolver {
	return CredentialResolver{store: store, systemToken: strings.TrimSpace(systemToken)}
}

func (r CredentialResolver) Resolve(ctx context.Context, req CredentialRequest) (Credential, error) {
	if req.ChatType == ChatTypeGroup && req.ChatID != "" {
		if cred, err := r.find(ctx, req, CredentialScope{Type: ScopeChat, ID: req.ChatID}); err == nil {
			return cred, nil
		} else if !errors.Is(err, ErrCredentialNotFound) {
			return Credential{}, err
		}
	}

	if req.OpenID != "" {
		if cred, err := r.find(ctx, req, CredentialScope{Type: ScopePersonal, ID: req.OpenID}); err == nil {
			return cred, nil
		} else if !errors.Is(err, ErrCredentialNotFound) {
			return Credential{}, err
		}
	}

	if r.systemToken != "" {
		return Credential{
			Provider:  ProviderLuckin,
			Scope:     CredentialScope{Type: ScopeSystem},
			Token:     r.systemToken,
			TokenHint: MaskToken(r.systemToken),
		}, nil
	}
	return Credential{}, ErrCredentialNotFound
}

func (r CredentialResolver) find(ctx context.Context, req CredentialRequest, scope CredentialScope) (Credential, error) {
	if r.store == nil {
		return Credential{}, ErrCredentialNotFound
	}
	return r.store.FindToken(ctx, CredentialLookup{
		Provider:  ProviderLuckin,
		AppID:     req.AppID,
		BotOpenID: req.BotOpenID,
		Scope:     scope,
	})
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}
