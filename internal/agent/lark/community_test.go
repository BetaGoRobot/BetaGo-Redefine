package lark

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
)

func communityClient(t *testing.T, f transportFunc) *Client {
	t.Helper()
	c, err := NewClient(ClientConfig{AppID: "app", AppSecret: "secret", BotOpenID: "bot", HTTPClient: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/auth/") {
			return response(`{"code":0,"tenant_access_token":"test-token","expire":7200}`), nil
		}
		return f(r)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func providerMessage(group, typ, content string) *http.Response {
	b, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"message_id": "message", "chat_id": group, "msg_type": typ, "create_time": "1792649990000", "update_time": "1792650000000", "sender": map[string]any{"id": "alice", "id_type": "open_id", "sender_type": "user", "tenant_key": "tenant"}, "body": map[string]any{"content": content}}}}})
	return response(string(b))
}
func TestCommunityMembersScopeTamperAndWholeProviderPage(t *testing.T) {
	calls := 0
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("member_id_type") != "open_id" || r.URL.Query().Get("page_size") != "1" {
			t.Error("provider bounds missing")
		}
		return response(`{"code":0,"data":{"items":[{"member_id":"a","member_id_type":"open_id","name":"A"},{"member_id":"b","member_id_type":"open_id","name":"B"}],"has_more":true,"page_token":"provider-cursor","member_total":3}}`), nil
	})
	p, err := c.GroupMembers(context.Background(), clientScope(), 1, "")
	if err != nil || len(p.Members) != 2 || !p.HasMore || p.PageToken == "provider-cursor" {
		t.Fatalf("same-join batch was clipped or cursor exposed: %+v %v", p, err)
	}
	other := clientScope()
	other.ChatID = "other"
	for _, v := range []struct {
		s     domain.Scope
		token string
	}{{other, p.PageToken}, {clientScope(), p.PageToken + "x"}, {clientScope(), "provider-cursor"}} {
		if _, err := c.GroupMembers(context.Background(), v.s, 1, v.token); err == nil {
			t.Error("foreign/tampered cursor accepted")
		}
	}
	if calls != 1 {
		t.Fatal("bad cursor reached provider")
	}
	if _, err := c.GroupMembers(context.Background(), clientScope(), 1, p.PageToken); err != nil {
		t.Fatal(err)
	}
	otherClient := communityClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("other client cursor reached provider")
		return nil, nil
	})
	if _, err := otherClient.GroupMembers(context.Background(), clientScope(), 1, p.PageToken); err == nil {
		t.Fatal("cursor crossed client")
	}
}
func TestCommunityEffectsVerifyGroupAndOnlyDeleteOwnReaction(t *testing.T) {
	group := "other"
	writes := 0
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/reactions") && r.Method == "GET" {
			return response(`{"code":0,"data":{"items":[{"reaction_id":"foreign","operator":{"operator_id":"alice","operator_type":"user"},"reaction_type":{"emoji_type":"THUMBSUP"}},{"reaction_id":"own","operator":{"operator_id":"app","operator_type":"app"},"reaction_type":{"emoji_type":"THUMBSUP"}}],"has_more":false}}`), nil
		}
		if r.Method == "GET" {
			return providerMessage(group, "text", `{"text":"hello"}`), nil
		}
		writes++
		if r.Method == "DELETE" && !strings.HasSuffix(r.URL.Path, "/own") {
			t.Error("deleted foreign reaction")
		}
		return response(`{"code":0,"data":{"reaction_id":"own","operator":{"operator_id":"app","operator_type":"app"},"reaction_type":{"emoji_type":"THUMBSUP"}}}`), nil
	})
	if _, err := c.AddReaction(context.Background(), clientScope(), "message", "THUMBSUP", "uuid"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("cross-group write not rejected as authorization: %v", err)
	}
	if err := c.RemoveReaction(context.Background(), clientScope(), "message", "own", "uuid"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatal("cross-group side effect")
	}
	group = "group"
	if err := c.RemoveReaction(context.Background(), clientScope(), "message", "foreign", "uuid"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("foreign reaction deletion accepted: %v", err)
	}
	if writes != 0 {
		t.Fatal("foreign deletion reached provider")
	}
	if err := c.RemoveReaction(context.Background(), clientScope(), "message", "own", "uuid"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddReaction(context.Background(), clientScope(), "message", "THUMBSUP", "uuid"); err != nil {
		t.Fatal(err)
	}
	if writes != 2 {
		t.Fatal("own effects unavailable")
	}
}
func TestCommunityImageVerifiesSnapshotBytesAndReuploads(t *testing.T) {
	payload := []byte("real-image-bytes")
	uploads, sends := 0, 0
	group := "group"
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/resources/") {
			resp := response("")
			resp.Header.Set("Content-Type", "image/png")
			resp.Body = io.NopCloser(strings.NewReader(string(payload)))
			return resp, nil
		}
		if r.Method == "GET" {
			return providerMessage(group, "image", `{"image_key":"received-key"}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/images") {
			uploads++
			if err := r.ParseMultipartForm(11 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("image_type") != "message" {
				t.Error("wrong upload type")
			}
			f, _, err := r.FormFile("image")
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(f)
			f.Close()
			if string(b) != string(payload) {
				t.Error("upload did not use actual bytes")
			}
			return response(`{"code":0,"data":{"image_key":"uploaded-key"}}`), nil
		}
		sends++
		var body struct{ Content, Uuid string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Uuid != "stable-image-uuid" || !strings.Contains(body.Content, "uploaded-key") || strings.Contains(body.Content, "received-key") {
			t.Errorf("image UUID/key lost: %+v", body)
		}
		return response(`{"code":0,"data":{"message_id":"sent"}}`), nil
	})
	m, err := c.ReadMessage(context.Background(), clientScope(), "message")
	if err != nil {
		t.Fatal(err)
	}
	changed := m.Clone()
	changed.Source.Version--
	if _, err := c.SendImage(context.Background(), clientScope(), changed, m.Media[0], "message", "stable-image-uuid"); !errors.Is(err, domain.ErrStaleSource) {
		t.Fatalf("stale image accepted: %v", err)
	}
	group = "other"
	if _, err := c.SendImage(context.Background(), clientScope(), m, m.Media[0], "message", "stable-image-uuid"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal(err)
	}
	group = "group"
	for _, bad := range [][]byte{nil, make([]byte, (10<<20)+1)} {
		payload = bad
		if _, err := c.SendImage(context.Background(), clientScope(), m, m.Media[0], "message", "stable-image-uuid"); err == nil {
			t.Fatal("empty/large image accepted")
		}
	}
	if uploads+sends != 0 {
		t.Fatal("invalid image caused write")
	}
	payload = []byte("real-image-bytes")
	id, err := c.SendImage(context.Background(), clientScope(), m, m.Media[0], "message", "stable-image-uuid")
	if err != nil || id != "sent" || uploads != 1 || sends != 1 {
		t.Fatalf("image failed: %s %v", id, err)
	}
}

func TestCommunitySDKAuthorizationAndCancellationAreNotUnavailable(t *testing.T) {
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		p := response(`{"code":99991679,"msg":"permission denied"}`)
		p.StatusCode = http.StatusForbidden
		return p, nil
	})
	for _, run := range []func() error{
		func() error { _, e := c.GroupInfo(context.Background(), clientScope()); return e },
		func() error { _, e := c.GroupMembers(context.Background(), clientScope(), 50, ""); return e },
		func() error {
			_, e := c.ListReactions(context.Background(), clientScope(), "message", 50, "")
			return e
		},
		func() error {
			_, e := c.AddReaction(context.Background(), clientScope(), "message", "THUMBSUP", "uuid")
			return e
		},
	} {
		if e := run(); !errors.Is(e, domain.ErrUnauthorized) {
			t.Fatalf("SDK authorization swallowed: %v", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.GroupInfo(ctx, clientScope()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestCommunitySDKStructuredPermissionsAndMissingSourcePropagate(t *testing.T) {
	permission := true
	c := communityClient(t, func(*http.Request) (*http.Response, error) {
		if permission {
			return response(`{"code":900001,"error":{"permission_violations":[{"type":"scope","subject":"im:message"}]}}`), nil
		}
		return response(`{"code":0,"data":{"items":[]}}`), nil
	})
	if _, err := c.GroupInfo(context.Background(), clientScope()); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("structured permission swallowed: %v", err)
	}
	permission = false
	if _, err := c.ReadMessage(context.Background(), clientScope(), "message"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing original swallowed: %v", err)
	}
}

func TestCommunityCurrentMessageReadRejectsForeignScopeAsAuthorization(t *testing.T) {
	c := communityClient(t, func(*http.Request) (*http.Response, error) {
		return providerMessage("foreign", "text", `{"text":"private"}`), nil
	})
	if _, err := c.ReadMessage(context.Background(), clientScope(), "message"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("foreign source did not preserve authorization failure: %v", err)
	}
}

func TestCommunityImageDownloadPermissionIsAuthorization(t *testing.T) {
	writes := 0
	c := communityClient(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/resources/") {
			p := response(`{"code":900001,"error":{"permission_violations":[{"type":"scope"}]}}`)
			p.StatusCode = http.StatusForbidden
			return p, nil
		}
		if r.Method != "GET" {
			writes++
			return response(`{"code":0}`), nil
		}
		return providerMessage("group", "image", `{"image_key":"image"}`), nil
	})
	m, err := c.ReadMessage(context.Background(), clientScope(), "message")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendImage(context.Background(), clientScope(), m, m.Media[0], "message", "uuid"); !errors.Is(err, domain.ErrUnauthorized) || writes != 0 {
		t.Fatalf("download permission lost: %v writes=%d", err, writes)
	}
}
