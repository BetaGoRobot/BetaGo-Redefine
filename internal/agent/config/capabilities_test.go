package config

import (
	"strings"
	"testing"
)

func TestCapabilityConfigurationDoesNotEnableUnconfiguredProviders(t *testing.T) {
	c := Capabilities{}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Capabilities{
		{MusicURL: "https://user:secret@music.example.test"},
		{LuckinURL: "https://coffee.example.test", LuckinSigningKey: "short"},
		{AudioEnabled: true},
		{Admins: []CapabilityPrincipal{{ActorID: "admin"}}},
		{LuckinAccounts: []CapabilityAccount{{CapabilityPrincipal: CapabilityPrincipal{TenantID: "t", ChatID: "c", ActorID: "a"}, Token: "secret"}}},
	} {
		if err := c.validate(); err == nil {
			t.Fatalf("accepted invalid capability configuration")
		}
	}
}

func TestCapabilitiesDecodeManagedAccountsWithoutLegacyConfiguration(t *testing.T) {
	raw := example(t) + `\n`
	raw = strings.TrimSuffix(raw, `\n`) + "\n" + `
[capabilities]
music_url="http://127.0.0.1:3000"
luckin_url="https://coffee.example.test"
luckin_signing_key="01234567890123456789012345678901"
luckin_credentials_key="a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
[[capabilities.admins]]
tenant_id="t"
chat_id="c"
actor_id="admin"
`
	c, err := Decode(strings.NewReader(raw), "agent", allEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Capabilities.LuckinAccounts) != 0 || len(c.Capabilities.LuckinCredentialsKey) != 64 || len(c.Capabilities.Admins) != 1 || c.Capabilities.Admins[0].ActorID != "admin" {
		t.Fatal("account principal did not decode")
	}
}

func TestCapabilityCredentialsAndBootstrapAdminsRemainServerScoped(t *testing.T) {
	c := Capabilities{MusicURL: "http://127.0.0.1:3000", LuckinURL: "https://coffee.example.test", LuckinSigningKey: "01234567890123456789012345678901", LuckinCredentialsKey: strings.Repeat("a1", 32), Admins: []CapabilityPrincipal{{TenantID: "t", ChatID: "c", ActorID: "a"}}}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.Admins = append(c.Admins, c.Admins[0])
	if c.validate() == nil {
		t.Fatal("duplicate administrator scope accepted")
	}
}

func TestManagedLuckinAccountsRequireDedicatedEncryptionKeyWithoutStaticTokens(t *testing.T) {
	c := Capabilities{LuckinURL: "https://coffee.example.test", LuckinSigningKey: strings.Repeat("s", 32), LuckinCredentialsKey: strings.Repeat("a1", 32)}
	if c.validate() != nil {
		t.Fatal("empty managed account database must permit first private binding")
	}
	for _, key := range []string{"short", strings.Repeat("zz", 32), strings.Repeat("a", 63)} {
		bad := c
		bad.LuckinCredentialsKey = key
		if bad.validate() == nil {
			t.Fatal("invalid encryption key accepted")
		}
	}
	c.LuckinAccounts = []CapabilityAccount{{CapabilityPrincipal: CapabilityPrincipal{TenantID: "t", ChatID: "c", ActorID: "a"}, Token: "static-token"}}
	if c.validate() == nil {
		t.Fatal("managed credentials must not silently fall back to static tokens")
	}
	c.LuckinAccounts = nil
	c.LuckinURL = ""
	if c.validate() == nil {
		t.Fatal("orphan managed credential key accepted")
	}
}

func TestMusicPlayerIsExplicitSafePublicConfiguration(t *testing.T) {
	c := Capabilities{MusicURL: "http://music:3335", MusicPlayerURL: "https://player.example.test:2443"}
	if c.validate() != nil {
		t.Fatal("explicit existing music player rejected")
	}
	for _, url := range []string{"javascript:play()", "https://user:password@player.example.test", "https://player.example.test?target=override", "https://player.example.test/#fragment"} {
		bad := c
		bad.MusicPlayerURL = url
		if bad.validate() == nil {
			t.Fatal("unsafe player base accepted")
		}
	}
	c.MusicURL = ""
	if c.validate() == nil {
		t.Fatal("player configured without music provider")
	}
}
