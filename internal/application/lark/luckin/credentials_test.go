package luckin

import (
	"context"
	"errors"
	"testing"
)

type fakeCredentialStore struct {
	values map[CredentialLookup]string
	err    error
}

func (f fakeCredentialStore) FindToken(ctx context.Context, lookup CredentialLookup) (Credential, error) {
	if f.err != nil {
		return Credential{}, f.err
	}
	token := f.values[lookup]
	if token == "" {
		return Credential{}, ErrCredentialNotFound
	}
	return Credential{Provider: ProviderLuckin, Scope: lookup.Scope, Token: token, TokenHint: MaskToken(token)}, nil
}

func TestResolverUsesPersonalToken(t *testing.T) {
	store := fakeCredentialStore{values: map[CredentialLookup]string{
		{Provider: ProviderLuckin, AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopePersonal, ID: "user"}}: "user-token",
	}}
	resolver := NewCredentialResolver(store, "")

	cred, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypeGroup,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if cred.Token != "user-token" || cred.Scope.Type != ScopePersonal {
		t.Fatalf("credential mismatch: scope=%s hint=%s", cred.Scope.Type, cred.TokenHint)
	}
}

func TestResolverIgnoresChatAndSystemTokens(t *testing.T) {
	// 即使存在群聊 token，也只解析个人；个人不存在则返回未找到（不再回退系统）。
	store := fakeCredentialStore{values: map[CredentialLookup]string{
		{Provider: ProviderLuckin, AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopeChat, ID: "chat"}}: "chat-token",
	}}
	resolver := NewCredentialResolver(store, "system-token")
	if _, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypeGroup,
	}); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

func TestResolverPrivateUsesPersonalFirst(t *testing.T) {
	store := fakeCredentialStore{values: map[CredentialLookup]string{
		{Provider: ProviderLuckin, AppID: "app", BotOpenID: "bot", Scope: CredentialScope{Type: ScopePersonal, ID: "user"}}: "user-token",
	}}
	resolver := NewCredentialResolver(store, "")

	cred, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypePrivate,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if cred.Token != "user-token" || cred.Scope.Type != ScopePersonal {
		t.Fatalf("credential mismatch: scope=%s hint=%s", cred.Scope.Type, cred.TokenHint)
	}
}

func TestResolverReturnsCredentialNotFound(t *testing.T) {
	resolver := NewCredentialResolver(fakeCredentialStore{values: map[CredentialLookup]string{}}, "")

	_, err := resolver.Resolve(context.Background(), CredentialRequest{
		AppID: "app", BotOpenID: "bot", ChatID: "chat", OpenID: "user", ChatType: ChatTypeGroup,
	})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("err = %v, want ErrCredentialNotFound", err)
	}
}

func TestMaskTokenReturnsOnlyHint(t *testing.T) {
	if got := MaskToken("abcdef123456"); got != "****3456" {
		t.Fatalf("MaskToken long = %q", got)
	}
	if got := MaskToken("abc"); got != "****" {
		t.Fatalf("MaskToken short = %q", got)
	}
	if got := MaskToken(""); got != "" {
		t.Fatalf("MaskToken empty = %q", got)
	}
}
