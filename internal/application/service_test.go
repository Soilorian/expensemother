package application_test

import (
	"context"
	"expensemother/internal/adapters/memory"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	"expensemother/internal/shared"
	"sync"
	"testing"
	"time"
)

type setupMembership struct{ admin bool }

func (m setupMembership) Member(context.Context, int64, int64) (bool, bool, error) {
	return true, m.admin, nil
}

func TestLinkExistingGroupRequiresBothAdministratorRoles(t *testing.T) {
	s, a, b, _, g := scenario(t)
	ctx := context.Background()
	s.Telegram = setupMembership{false}
	if _, err := s.Setup(ctx, a, -100, "Chat", g); err == nil {
		t.Fatal("non-Telegram-admin linked group")
	}
	s.Telegram = setupMembership{true}
	if _, err := s.Setup(ctx, b, -100, "Chat", g); err == nil {
		t.Fatal("non-application-admin linked group")
	}
	linked, err := s.Setup(ctx, a, -100, "Chat", g)
	if err != nil || linked.ID != g || linked.ChatID != -100 {
		t.Fatal(linked, err)
	}
	if _, err := s.Setup(ctx, a, -200, "Other chat", g); err == nil {
		t.Fatal("group linked to two chats")
	}
}

func scenario(t *testing.T) (*app.Service, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	s := app.New(memory.New())
	a, e := s.TelegramUser(ctx, 1, "Alice", "", "en", "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.TelegramUser(ctx, 2, "Bob", "", "en", "")
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.TelegramUser(ctx, 3, "Cyrus", "", "en", "")
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.SaveGroup(ctx, a.ID, "", &gr.Group{Name: "Trip", Members: []gr.Member{{UserID: b.ID, Active: true}, {UserID: c.ID, Active: true}}})
	if e != nil {
		t.Fatal(e)
	}
	return s, a.ID, b.ID, c.ID, g.ID
}
func expense(t *testing.T, s *app.Service, a, b, c, g string) *ex.Expense {
	t.Helper()
	n := shared.Money(1200000)
	now := time.Now()
	e, err := s.SaveExpense(context.Background(), a, "", &ex.Expense{GroupID: g, Type: "COST", State: "FINAL", Payer: a, Amount: &n, PaidOn: &now, Shares: []ex.Share{{UserID: a}, {UserID: b}, {UserID: c}}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestPartialPaymentAndUndo(t *testing.T) {
	s, a, b, c, g := scenario(t)
	ctx := context.Background()
	e := expense(t, s, a, b, c, g)
	n := shared.Money(150000)
	p, err := s.RecordPayment(ctx, b, e.ID, "", &n, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Summary(ctx, b, g)
	if err != nil || v.Owed != 250000 || v.Balance != -250000 {
		t.Fatal(v, err)
	}
	if _, err = s.SaveExpense(ctx, a, e.ID, e); err == nil {
		t.Fatal("edited paid expense")
	}
	if err = s.Reverse(ctx, b, e.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.Summary(ctx, b, g)
	if err != nil || v.Owed != 400000 {
		t.Fatal(v, err)
	}
}
func TestConcurrentPayCannotOverpay(t *testing.T) {
	s, a, b, c, g := scenario(t)
	e := expense(t, s, a, b, c, g)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RecordPayment(context.Background(), b, e.ID, "", nil, nil)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("%d payments posted", success)
	}
}
func TestOutsiderIsolationAndClaimOwnership(t *testing.T) {
	s, a, b, _, g := scenario(t)
	ctx := context.Background()
	e, err := s.SaveExpense(ctx, a, "", &ex.Expense{Type: "COST", State: "DRAFT", GroupID: g, Payer: a, Shares: []ex.Share{{UserID: a}}})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := s.TelegramUser(ctx, 9, "outsider", "", "", "")
	if _, err = s.Expense(ctx, x.ID, e.ID); err == nil {
		t.Fatal("outsider read")
	}
	if _, err = s.MutateDraft(ctx, x.ID, e.ID, func(e *ex.Expense) error { return e.Join(x.ID, true, "") }); err == nil {
		t.Fatal("outsider joined")
	}
	e.Name = "hijacked"
	if _, err = s.SaveExpense(ctx, b, e.ID, e); err == nil {
		t.Fatal("noncreator edited")
	}
}
func TestIncomeAndHistoricalMembership(t *testing.T) {
	s, a, b, _, g := scenario(t)
	ctx := context.Background()
	n := shared.Money(600000)
	now := time.Now()
	e, err := s.SaveExpense(ctx, b, "", &ex.Expense{Type: "INCOME", State: "FINAL", GroupID: g, Receiver: b, Amount: &n, PaidOn: &now, Shares: []ex.Share{{UserID: a}, {UserID: b}}})
	if err != nil {
		t.Fatal(err)
	}
	ds, err := s.Debts(ctx, b)
	if err != nil || len(ds) != 1 || ds[0].Creditor != a || ds[0].Remaining != 300000 {
		t.Fatal(ds, err)
	}
	group, _ := s.Group(ctx, a, g)
	group.Member(b).Active = false
	if _, err = s.SaveGroup(ctx, a, g, group); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordPayment(ctx, b, e.ID, "", nil, nil); err != nil {
		t.Fatal("former member cannot settle", err)
	}
}
func TestTelegramIdentityLinkConflict(t *testing.T) {
	s, a, b, _, _ := scenario(t)
	ctx := context.Background()
	again, e := s.TelegramUser(ctx, 1, "Alice", "issuer|sub", "", "")
	if e != nil || again.ID != a {
		t.Fatal(again, e)
	}
	if _, e = s.TelegramUser(ctx, 1, "", "issuer|sub", "", b); e == nil {
		t.Fatal("merged two accounts")
	}
}
