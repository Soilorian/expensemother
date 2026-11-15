package telegram

import (
	"context"
	"encoding/json"
	"expensemother/internal/adapters/memory"
	app "expensemother/internal/application"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGroupBotDraftJoinFinalizePayAndConfirmationOwnership(t *testing.T) {
	var mu sync.Mutex
	sent := []map[string]any{}
	next := int64(100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		result := any(true)
		if strings.HasSuffix(r.URL.Path, "getChatMember") {
			result = map[string]any{"status": "administrator"}
		}
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			mu.Lock()
			sent = append(sent, in)
			next++
			result = map[string]any{"message_id": next}
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	client := NewClient("fake")
	client.BaseURL = server.URL
	svc := app.New(memory.New())
	svc.Telegram = client
	bot := &Bot{Client: client, Service: svc}
	ctx := context.Background()
	chat := Chat{ID: -100, Type: "supergroup", Title: "Trip"}
	alice := User{ID: 1, First: "Alice"}
	bob := User{ID: 2, First: "Bob"}
	send := func(u User, text string) {
		t.Helper()
		if e := bot.Handle(ctx, Update{Message: &Message{ID: 1, From: u, Chat: chat, Text: text}}); e != nil {
			t.Fatal(e)
		}
	}
	click := func(u User, data string) error {
		return bot.Handle(ctx, Update{Callback: &Callback{ID: "cb", From: u, Message: &Message{ID: 1, Chat: chat}, Data: data}})
	}
	send(alice, "/setup")
	send(bob, "/join")
	send(alice, "/expense Dinner 1200000")
	a, _ := svc.TelegramUser(ctx, 1, "Alice", "", "", "")
	b, _ := svc.TelegramUser(ctx, 2, "Bob", "", "", "")
	es, e := svc.Expenses(ctx, a.ID)
	if e != nil || len(es) != 1 {
		t.Fatal(es, e)
	}
	expense := es[0]
	if e = click(bob, "join:"+expense.ID); e != nil {
		t.Fatal(e)
	}
	send(alice, "/orders")
	send(bob, "/mytotal")
	if e = click(alice, "final:"+expense.ID); e != nil {
		t.Fatal(e)
	}
	find := func(action string) string {
		for k, c := range bot.confirm {
			if c.Action == action {
				return k
			}
		}
		t.Fatalf("no %s confirmation", action)
		return ""
	}
	token := find("final")
	if e = click(bob, "confirm:"+token); e == nil {
		t.Fatal("another member confirmed finalization")
	}
	if e = click(alice, "confirm:"+token); e != nil {
		t.Fatal(e)
	}
	if e = click(bob, "pay:"+expense.ID); e != nil {
		t.Fatal(e)
	}
	if e = click(bob, "payto:"+expense.ID+":0"); e != nil {
		t.Fatal(e)
	}
	token = find("pay")
	if e = click(bob, "confirm:"+token); e != nil {
		t.Fatal(e)
	}
	if e = click(bob, "confirm:"+token); e == nil {
		t.Fatal("confirmation posted twice")
	}
	ds, e := svc.Debts(ctx, b.ID)
	if e != nil || len(ds) != 0 {
		t.Fatal(ds, e)
	}
	if len(sent) < 6 {
		t.Fatal("missing bot UI messages")
	}
	for _, m := range sent {
		b, _ := json.Marshal(m)
		if strings.Contains(string(b), "passwordHash") || strings.Contains(string(b), "Bearer") {
			t.Fatal("identity secret in chat")
		}
	}
}
func TestReceiptAmountNormalization(t *testing.T) {
	for _, v := range []string{"۱۲۰٬۰۰۰", "120,000", "١٢٠٠٠٠"} {
		n, e := parseAmount(v)
		if e != nil || n != 120000 {
			t.Fatal(v, n, e)
		}
	}
	for _, v := range []string{"12,34", "1e999999999", "12.5"} {
		if _, e := parseAmount(v); e == nil {
			t.Fatal("invalid amount accepted", v)
		}
	}
}
