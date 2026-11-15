package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	app "expensemother/internal/application"
	id "expensemother/internal/identity/domain"
	"expensemother/internal/shared"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Auth struct {
	Service            *app.Service
	Secret             []byte
	BotToken, ClientID string
	Client             *http.Client
	JWKSURL            string
	mu                 sync.Mutex
	keys               map[string]*rsa.PublicKey
	keysUntil          time.Time
}

func New(s *app.Service, secret, bot, clientID string) *Auth {
	return &Auth{Service: s, Secret: []byte(secret), BotToken: bot, ClientID: clientID, Client: &http.Client{Timeout: 10 * time.Second}, JWKSURL: "https://oauth.telegram.org/.well-known/jwks.json"}
}
func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func mac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func encode(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
func (a *Auth) Session(u *id.User) map[string]any {
	now := time.Now().Unix()
	payload := encode(map[string]any{"sub": u.ID, "iss": "expensemother", "aud": "expensemother-api", "iat": now, "exp": now + 3600})
	t := encode(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + payload
	t += "." + base64.RawURLEncoding.EncodeToString(mac(a.Secret, t))
	var email any
	if u.Email != "" {
		email = u.Email
	}
	return map[string]any{"userId": u.ID, "displayName": u.Name, "email": email, "token": t, "tokenType": "Bearer", "expiresIn": 3600}
}
func (a *Auth) Actor(ctx context.Context, token string) (string, error) {
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return "", shared.Unauthorized()
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[2])
	if e != nil || !hmac.Equal(sig, mac(a.Secret, p[0]+"."+p[1])) {
		return "", shared.Unauthorized()
	}
	var h struct{ Alg string }
	if decode(p[0], &h) != nil || h.Alg != "HS256" {
		return "", shared.Unauthorized()
	}
	var c struct {
		Sub, Iss, Aud string
		Exp           int64
	}
	if decode(p[1], &c) != nil || c.Iss != "expensemother" || c.Aud != "expensemother-api" || c.Exp <= time.Now().Unix() || !shared.ValidID(c.Sub) {
		return "", shared.Unauthorized()
	}
	u, e := a.Service.User(ctx, c.Sub)
	if e != nil || u.Guest {
		return "", shared.Unauthorized()
	}
	return u.ID, nil
}
func decode(s string, v any) error {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (a *Auth) Challenge(ctx context.Context) (map[string]any, error) {
	if a.ClientID == "" {
		return nil, shared.Bad("Telegram Login client ID is not configured")
	}
	key, nonce := random(), random()
	err := a.Service.Store.Within(ctx, func(r app.Repository) error { return r.PutProof("login:"+key, nonce, time.Now().Add(5*time.Minute)) })
	return map[string]any{"challengeId": key, "nonce": nonce, "clientId": a.ClientID, "expiresIn": 300}, err
}
func (a *Auth) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keysUntil.After(time.Now()) && a.keys[kid] != nil {
		return a.keys[kid], nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", a.JWKSURL, nil)
	if e != nil {
		return nil, e
	}
	resp, e := a.Client.Do(req)
	if e != nil {
		return nil, shared.Unauthorized()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, shared.Unauthorized()
	}
	var jwks struct {
		Keys []struct{ Kty, Kid, N, E, Alg, Use string }
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&jwks); e != nil {
		return nil, shared.Unauthorized()
	}
	a.keys = map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, er := base64.RawURLEncoding.DecodeString(k.N)
		if er != nil {
			continue
		}
		b, er := base64.RawURLEncoding.DecodeString(k.E)
		if er != nil || len(b) > 4 {
			continue
		}
		exp := 0
		for _, x := range b {
			exp = exp*256 + int(x)
		}
		if exp < 3 || len(n) < 256 {
			continue
		}
		a.keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	a.keysUntil = time.Now().Add(time.Hour)
	if a.keys[kid] == nil {
		return nil, shared.Unauthorized()
	}
	return a.keys[kid], nil
}
func (a *Auth) Login(ctx context.Context, challenge, token, link string) (*id.User, error) {
	if len(token) > 32768 || a.ClientID == "" {
		return nil, shared.Unauthorized()
	}
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return nil, shared.Unauthorized()
	}
	var h struct{ Alg, Kid string }
	if decode(p[0], &h) != nil || h.Alg != "RS256" || h.Kid == "" {
		return nil, shared.Unauthorized()
	}
	key, e := a.key(ctx, h.Kid)
	if e != nil {
		return nil, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[2])
	if e != nil {
		return nil, shared.Unauthorized()
	}
	digest := sha256.Sum256([]byte(p[0] + "." + p[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return nil, shared.Unauthorized()
	}
	var c struct {
		Iss, Sub, Nonce, Name string
		Aud                   json.RawMessage
		ID                    int64 `json:"id"`
		Exp, Iat              int64
	}
	if decode(p[1], &c) != nil || c.Iss != "https://oauth.telegram.org" || c.Sub == "" || c.ID <= 0 || c.Exp <= time.Now().Unix() || c.Iat > time.Now().Add(30*time.Second).Unix() {
		return nil, shared.Unauthorized()
	}
	var single string
	var multiple []string
	validAud := false
	if json.Unmarshal(c.Aud, &single) == nil {
		validAud = single == a.ClientID
	} else if json.Unmarshal(c.Aud, &multiple) == nil {
		for _, v := range multiple {
			if v == a.ClientID {
				validAud = true
			}
		}
	}
	if !validAud {
		return nil, shared.Unauthorized()
	}
	e = a.Service.Store.Within(ctx, func(r app.Repository) error {
		nonce, e := r.ConsumeProof("login:" + challenge)
		if e != nil {
			return e
		}
		if !hmac.Equal([]byte(nonce), []byte(c.Nonce)) {
			return shared.Unauthorized()
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return a.Service.TelegramUser(ctx, c.ID, c.Name, c.Iss+"|"+c.Sub, "", link)
}
func (a *Auth) MiniApp(ctx context.Context, raw, link string) (*id.User, error) {
	if a.BotToken == "" || len(raw) > 32768 {
		return nil, shared.Unauthorized()
	}
	v, e := url.ParseQuery(raw)
	if e != nil {
		return nil, shared.Unauthorized()
	}
	for _, values := range v {
		if len(values) != 1 {
			return nil, shared.Unauthorized()
		}
	}
	hash, e := hex.DecodeString(v.Get("hash"))
	if e != nil || len(hash) != 32 {
		return nil, shared.Unauthorized()
	}
	v.Del("hash")
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	secret := mac([]byte("WebAppData"), a.BotToken)
	if !hmac.Equal(hash, mac(secret, strings.Join(lines, "\n"))) {
		return nil, shared.Unauthorized()
	}
	date, e := strconv.ParseInt(v.Get("auth_date"), 10, 64)
	now := time.Now()
	if e != nil || date < now.Add(-5*time.Minute).Unix() || date > now.Add(30*time.Second).Unix() {
		return nil, shared.Unauthorized()
	}
	var u struct {
		ID    int64
		First string `json:"first_name"`
		Last  string `json:"last_name"`
		Lang  string `json:"language_code"`
	}
	if json.Unmarshal([]byte(v.Get("user")), &u) != nil || u.ID <= 0 {
		return nil, shared.Unauthorized()
	}
	e = a.Service.Store.Within(ctx, func(r app.Repository) error {
		return r.PutProof("mini:"+hex.EncodeToString(hash), "used", now.Add(6*time.Minute))
	})
	if e != nil {
		return nil, shared.Unauthorized()
	}
	return a.Service.TelegramUser(ctx, u.ID, strings.TrimSpace(u.First+" "+u.Last), "", u.Lang, link)
}
