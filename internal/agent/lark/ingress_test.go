package lark

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/domain"
	event "github.com/larksuite/oapi-sdk-go/v3/event"
)

func TestSDKDispatcherAcknowledgesOnlyAfterFactPersistence(t *testing.T) {
	committed := make(chan domain.Event, 1)
	ing, err := NewIngress(IngressConfig{Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot"}, AppSecret: "secret", Persist: func(ctx context.Context, e domain.Event) error { committed <- e; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(incoming(t, "text", `{"text":"群成员互聊"}`))
	if _, err := ing.Dispatcher().Do(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-committed:
		if e.Message.Source.AuthorID != "alice" || e.Message.Text != "群成员互聊" {
			t.Fatal("wrong fact committed")
		}
	default:
		t.Fatal("event acknowledged without persistence")
	}
}

func encryptedCallback(t *testing.T, key string, plain []byte) *http.Request {
	t.Helper()
	digest := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	buf := make([]byte, aes.BlockSize+len(padded))
	cipher.NewCBCEncrypter(block, buf[:aes.BlockSize]).CryptBlocks(buf[aes.BlockSize:], padded)
	body, _ := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(buf)})
	req := httptest.NewRequest("POST", "/lark/events", bytes.NewReader(body))
	req.Header.Set(event.EventRequestTimestamp, "1792650000")
	req.Header.Set(event.EventRequestNonce, "fixture")
	req.Header.Set(event.EventSignature, event.Signature("1792650000", "fixture", key, string(body)))
	return req
}

func TestHTTPCallbackRequiresSDKSignatureAndPersistsBeforeAcknowledgment(t *testing.T) {
	var committed int
	ing, err := NewIngress(IngressConfig{Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot"}, AppSecret: "secret", VerificationToken: "verify", EncryptionKey: "encrypt", Persist: func(ctx context.Context, e domain.Event) error { committed++; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(incoming(t, "text", `{"text":"verified group event"}`))
	bad := encryptedCallback(t, "encrypt", body)
	bad.Header.Set(event.EventSignature, "invalid")
	resp := httptest.NewRecorder()
	ing.Handler().ServeHTTP(resp, bad)
	if resp.Code < 400 || committed != 0 {
		t.Fatal("unsigned event reached persistence")
	}
	resp = httptest.NewRecorder()
	ing.Handler().ServeHTTP(resp, encryptedCallback(t, "encrypt", body))
	if resp.Code != 200 || committed != 1 {
		t.Fatalf("verified event not durably acknowledged: status=%d commits=%d", resp.Code, committed)
	}
	resp = httptest.NewRecorder()
	ing.Handler().ServeHTTP(resp, httptest.NewRequest("POST", "/lark/events", strings.NewReader(strings.Repeat("x", (1<<20)+1))))
	if resp.Code != http.StatusRequestEntityTooLarge || committed != 1 {
		t.Fatal("oversize callback reached SDK/persistence")
	}
	resp = httptest.NewRecorder()
	ing.Handler().ServeHTTP(resp, encryptedCallback(t, "encrypt", []byte(`{"type":"url_verification","token":"wrong","challenge":"challenge"}`)))
	if resp.Code < 400 || committed != 1 {
		t.Fatal("wrong challenge token accepted")
	}
}

func TestIngressPersistenceFailureRetriesWithoutLeakingError(t *testing.T) {
	ing, err := NewIngress(IngressConfig{Normalize: NormalizeConfig{AppID: "app", BotOpenID: "bot"}, AppSecret: "secret", Persist: func(ctx context.Context, e domain.Event) error {
		<-ctx.Done()
		return errors.New("private-database-credential")
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(incoming(t, "text", `{"text":"request"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = ing.Dispatcher().Do(ctx, b)
	if err == nil || err.Error() == "private-database-credential" || time.Since(start) > time.Second {
		t.Fatal("failed persistence was acknowledged, leaked or ignored cancellation")
	}
}
