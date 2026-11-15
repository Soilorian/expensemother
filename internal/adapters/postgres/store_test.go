package postgres_test

import (
	"context"
	"expensemother/internal/adapters/postgres"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	"expensemother/internal/shared"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresTransactionsPersistenceAndIdentity(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for real PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "expense_test_" + strings.ReplaceAll(shared.ID(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	st, e := postgres.Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer st.Pool.Close()
	if e = st.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = st.Migrate(ctx); e != nil {
		t.Fatal("migration rerun", e)
	}
	s := app.New(st)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.TelegramUser(ctx, 42, "Alice", "", "", "")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Error(err)
				return
			}
			ids[p.ID] = true
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatal("Telegram identity duplicated", ids)
	}
	var a string
	for id := range ids {
		a = id
	}
	bob, e := s.TelegramUser(ctx, 43, "Bob", "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.SaveGroup(ctx, a, "", &gr.Group{Name: "Integration", Members: []gr.Member{{UserID: bob.ID, Active: true}}})
	if e != nil {
		t.Fatal(e)
	}
	n := shared.Money(100)
	now := time.Now()
	expense, e := s.SaveExpense(ctx, a, "", &ex.Expense{GroupID: g.ID, Payer: a, State: "FINAL", Type: "COST", Amount: &n, PaidOn: &now, Shares: []ex.Share{{UserID: a}, {UserID: bob.ID}}})
	if e != nil {
		t.Fatal(e)
	}
	success := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RecordPayment(ctx, bob.ID, expense.ID, "", nil, nil)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			}
		}()
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("concurrent payment success count %d", success)
	}
	summary, e := s.Summary(ctx, bob.ID, g.ID)
	if e != nil || summary.Owed != 0 || summary.Payments != 50 {
		t.Fatal(summary, e)
	}
	second, e := postgres.Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer second.Pool.Close()
	read, e := app.New(second).Expense(ctx, a, expense.ID)
	if e != nil || read.Amount == nil || *read.Amount != 100 {
		t.Fatal("persistence failed", read, e)
	}
}
