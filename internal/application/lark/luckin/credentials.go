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

type CredentialWriter interface {
	UpsertToken(context.Context, CredentialLookup, string, string) error
	DeleteToken(context.Context, CredentialLookup, string) (bool, error)
}

type CredentialResolverFunc func(context.Context, CredentialRequest) (Credential, error)

type CredentialResolver struct {
	store CredentialStore
}

func NewCredentialResolver(store CredentialStore, systemToken string) CredentialResolver {
	// systemToken 参数保留以兼容调用方签名，但因涉及优惠券个人归属，已不再使用系统/群级凭证。
	_ = systemToken
	return CredentialResolver{store: store}
}

// Resolve 只解析发起人个人 token。出于优惠券归属与隐私考虑，不再支持系统默认或群聊默认凭证。
func (r CredentialResolver) Resolve(ctx context.Context, req CredentialRequest) (Credential, error) {
	if req.OpenID == "" {
		return Credential{}, ErrCredentialNotFound
	}
	return r.find(ctx, req, CredentialScope{Type: ScopePersonal, ID: req.OpenID})
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
