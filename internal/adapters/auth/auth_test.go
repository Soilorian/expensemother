package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"expensemother/internal/adapters/memory"
	app "expensemother/internal/application"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture() *Auth { return New(app.New(memory.New()), strings.Repeat("s", 32), "bot-secret", "123") }
func initData(a *Auth, date int64) string {
	v := url.Values{"auth_date": {strconv.FormatInt(date, 10)}, "user": {`{"id":42,"first_name":"Test"}`}}
	text := "auth_date=" + v.Get("auth_date") + "\nuser=" + v.Get("user")
	v.Set("hash", hex.EncodeToString(mac(mac([]byte("WebAppData"), a.BotToken), text)))
	return v.Encode()
}
func TestMiniAppTamperExpiryReplay(t *testing.T) {
	a := fixture()
	ctx := context.Background()
	raw := initData(a, time.Now().Unix())
	u, e := a.MiniApp(ctx, raw, "")
	if e != nil || u.TelegramID != 42 {
		t.Fatal(u, e)
	}
	if _, e = a.MiniApp(ctx, raw, ""); e == nil {
		t.Fatal("replay accepted")
	}
	if _, e = a.MiniApp(ctx, initData(a, time.Now().Add(-6*time.Minute).Unix()), ""); e == nil {
		t.Fatal("old proof accepted")
	}
	if _, e = a.MiniApp(ctx, raw+"&auth_date=1", ""); e == nil {
		t.Fatal("duplicate field accepted")
	}
	if _, e = a.Actor(ctx, a.Session(u)["token"].(string)); e != nil {
		t.Fatal(e)
	}
}
func TestOIDCValidatesSignatureNonceAndIdentity(t *testing.T) {
	a := fixture()
	ctx := context.Background()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer server.Close()
	a.JWKSURL = server.URL
	challenge, e := a.Challenge(ctx)
	if e != nil {
		t.Fatal(e)
	}
	claims := map[string]any{"iss": "https://oauth.telegram.org", "sub": "not-the-telegram-id", "id": 42, "aud": "123", "name": "Test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": challenge["nonce"]}
	sign := func() string {
		text := encode(map[string]string{"alg": "RS256", "kid": "test"}) + "." + encode(claims)
		hash := sha256.Sum256([]byte(text))
		sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if e != nil {
			t.Fatal(e)
		}
		return text + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	claims["nonce"] = "wrong"
	if _, e = a.Login(ctx, challenge["challengeId"].(string), sign(), ""); e == nil {
		t.Fatal("wrong nonce accepted")
	}
	claims["nonce"] = challenge["nonce"]
	u, e := a.Login(ctx, challenge["challengeId"].(string), sign(), "")
	if e != nil || u.TelegramID != 42 {
		t.Fatal(u, e)
	}
	if _, e = a.Login(ctx, challenge["challengeId"].(string), sign(), ""); e == nil {
		t.Fatal("replayed challenge accepted")
	}
}
