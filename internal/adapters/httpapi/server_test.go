package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"expensemother/internal/adapters/auth"
	"expensemother/internal/adapters/memory"
	"expensemother/internal/adapters/ocr"
	"expensemother/internal/adapters/receiptfiles"
	app "expensemother/internal/application"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type membership struct{}

func (membership) Member(context.Context, int64, int64) (bool, bool, error) { return true, true, nil }

type fixture struct {
	h http.Handler
	s *Server
	t *testing.T
}

func setup(t *testing.T) *fixture {
	t.Helper()
	svc := app.New(memory.New())
	svc.Telegram = membership{}
	a := auth.New(svc, strings.Repeat("a", 32), "fake-bot", "123")
	s := &Server{Service: svc, Auth: a, Files: &receiptfiles.Files{Root: t.TempDir(), Service: svc}, OCR: ocr.Tesseract{Binary: "nonexistent-test-tesseract"}}
	return &fixture{s.Handler(), s, t}
}
func (f *fixture) request(method, path, token string, body any, status int) map[string]any {
	f.t.Helper()
	var b bytes.Buffer
	if body != nil {
		if e := json.NewEncoder(&b).Encode(body); e != nil {
			f.t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, path, &b)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != status {
		f.t.Fatalf("%s %s: wanted %d got %d: %s", method, path, status, w.Code, w.Body.String())
	}
	out := map[string]any{}
	if status != 204 && w.Body.Len() > 0 {
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			f.t.Fatal(e)
		}
	}
	return out
}
func (f *fixture) signup(name string) (string, string) {
	v := f.request("POST", "/v1/auth/signup", "", map[string]any{"username": name, "password": "password123"}, 200)
	return v["userId"].(string), v["token"].(string)
}
func TestAPIGroupExpensePaymentLifecycle(t *testing.T) {
	f := setup(t)
	alice, at := f.signup("alice")
	bob, bt := f.signup("bobby")
	_, et := f.signup("outsider")
	f.request("POST", "/v1/auth/login", "", map[string]any{"username": "alice", "password": "password123"}, 200)
	profile := f.request("GET", "/v1/auth/profile", at, nil, 200)
	if _, ok := profile["token"]; ok {
		t.Fatal("profile leaked token")
	}
	g := f.request("POST", "/v1/groups", at, map[string]any{"displayName": "Trip", "users": []any{map[string]any{"userId": bob, "isAdmin": false}}}, 201)
	gid := g["groupId"].(string)
	f.request("GET", "/v1/groups", at, nil, 200)
	f.request("GET", "/v1/groups/"+gid, et, nil, 404)
	f.request("POST", "/v1/groups/"+gid+"/join", bt, nil, 200)
	guest := f.request("POST", "/v1/users", at, map[string]any{"groupId": gid, "displayName": "Guest"}, 201)
	f.request("PUT", "/v1/users/"+guest["userId"].(string), at, map[string]any{"displayName": "Guest renamed"}, 200)
	f.request("GET", "/v1/users/"+bob, at, nil, 200)
	public := f.request("GET", "/v1/users", at, nil, 200)
	for _, v := range public["elements"].([]any) {
		if _, ok := v.(map[string]any)["paymentInfos"]; ok {
			t.Fatal("private payment info in search")
		}
	}
	draft := map[string]any{"groupId": gid, "expenseType": "COST", "state": "DRAFT", "payerId": alice, "displayName": "Dinner", "amount": 1200000, "sharers": []any{map[string]any{"userId": alice}}}
	saved := f.request("POST", "/v1/expenses", at, draft, 201)
	eid := saved["expenseId"].(string)
	f.request("PUT", "/v1/expenses/"+eid+"/participation/me", bt, map[string]any{"participating": true}, 200)
	f.request("GET", "/v1/expenses/"+eid, et, nil, 404)
	sum := f.request("GET", "/v1/groups/"+gid+"/summary", at, nil, 200)
	if sum["totalSpending"].(float64) != 0 || sum["draftCount"].(float64) != 1 {
		t.Fatal(sum)
	}
	draft["sharers"] = []any{map[string]any{"userId": alice}, map[string]any{"userId": bob}}
	draft["state"] = "FINAL"
	draft["paidOn"] = time.Now().UTC().Format(time.RFC3339)
	f.request("PUT", "/v1/expenses/"+eid, at, draft, 200)
	f.request("PUT", "/v1/expenses/"+eid+"/participation/me", bt, map[string]any{"participating": false}, 409)
	payment := f.request("POST", "/v1/expenses/"+eid+"/payments", bt, map[string]any{"amount": 150000, "displayName": "bank transfer"}, 201)
	if payment["displayName"] != "bank transfer" {
		t.Fatal(payment)
	}
	f.request("PUT", "/v1/expenses/"+eid, at, draft, 409)
	ds := f.request("GET", "/v1/expenses/my-debt", bt, nil, 200)
	if ds["elements"].([]any)[0].(map[string]any)["remainingAmount"].(float64) != 450000 {
		t.Fatal(ds)
	}
	f.request("GET", "/v1/expenses/my-debt/payment-info", bt, nil, 200)
	f.request("DELETE", "/v1/expenses/"+eid+"/payments/"+payment["expenseId"].(string), bt, nil, 204)
	f.request("DELETE", "/v1/expenses/"+eid, at, nil, 204)
	rows := f.request("GET", "/v1/expenses", at, nil, 200)
	if rows["totalElements"].(float64) != 0 {
		t.Fatal(rows)
	}
	f.request("GET", "/v1/expenses?includeCancelled=true&size=1", at, nil, 200)
}
func TestAPIValidationAndIdentityProofRoutes(t *testing.T) {
	f := setup(t)
	uid, token := f.signup("alice")
	f.request("GET", "/v1/auth/profile", "", nil, 401)
	f.request("POST", "/v1/auth/telegram/challenge", "", nil, 200)
	f.request("POST", "/v1/auth/telegram/login", "", map[string]any{"challengeId": "x", "idToken": "forged"}, 401)
	f.request("POST", "/v1/auth/telegram/mini-app", "", map[string]any{"initData": "forged"}, 401)
	f.request("POST", "/v1/auth/telegram/link", token, map[string]any{"initData": "forged"}, 401)
	for _, amount := range []any{"1200000", 12.5, -1} {
		f.request("POST", "/v1/expenses", token, map[string]any{"expenseType": "COST", "state": "DRAFT", "amount": amount}, 400)
	}
	f.request("POST", "/v1/expenses", token, map[string]any{"expenseType": "COST", "state": "DRAFT", "auditInfo": map[string]any{}}, 400)
	f.request("POST", "/v1/expenses", token, map[string]any{"expenseType": "COST", "state": "DRAFT", "sharers": []any{map[string]any{"userId": uid, "isPayed": true}}}, 400)
	f.request("GET", "/v1/expenses?size=101", token, nil, 400)
	f.request("GET", "/v1/expenses/not-a-uuid", token, nil, 400)
}
func TestAPIReceiptPrivacyAndManualFallback(t *testing.T) {
	f := setup(t)
	_, token := f.signup("alice")
	_, other := f.signup("bobby")
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	im.Set(0, 0, color.White)
	var data bytes.Buffer
	png.Encode(&data, im)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("image", "receipt.png")
	part.Write(data.Bytes())
	mw.Close()
	r := httptest.NewRequest("POST", "/v1/receipts", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	rid := v["receiptId"].(string)
	if _, ok := v["path"]; ok {
		t.Fatal("private path exposed")
	}
	f.request("GET", "/v1/receipts/"+rid+"/image", other, nil, 404)
	x := f.request("POST", "/v1/receipts/"+rid+"/extract", token, map[string]any{"languages": []string{"fa", "en"}}, 200)
	if x["status"] != "FAILED" {
		t.Fatal(x)
	}
	r = httptest.NewRequest("GET", "/v1/receipts/"+rid+"/image", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data.Bytes()) {
		t.Fatal("receipt did not survive OCR failure")
	}
}
