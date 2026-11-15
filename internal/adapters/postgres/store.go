package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	id "expensemother/internal/identity/domain"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{p}, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	b, e := migrations.ReadFile("migrations/001_initial.sql")
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(b)); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) Within(ctx context.Context, fn func(app.Repository) error) error {
	for i := 0; i < 6; i++ {
		tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if e != nil {
			return e
		}
		e = fn(&repo{ctx, tx})
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e == nil {
			return nil
		}
		var pe *pgconn.PgError
		if errors.As(e, &pe) {
			if pe.Code == "40001" || pe.Code == "40P01" || pe.Code == "23505" {
				// Let the winning transaction finish before taking a new snapshot.
				timer := time.NewTimer(time.Duration(1<<i) * 10 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				continue
			}
			if pe.Code == "23503" {
				return shared.Bad("referenced record does not exist")
			}
		}
		return e
	}
	return shared.Conflict("record changed concurrently; refresh and retry")
}

type repo struct {
	ctx context.Context
	tx  pgx.Tx
}

func get[T any](r *repo, table, id string) (*T, error) {
	if !shared.ValidID(id) {
		return nil, shared.Missing()
	}
	var b []byte
	e := r.tx.QueryRow(r.ctx, "SELECT data FROM "+table+" WHERE id=$1 FOR UPDATE", id).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, shared.Missing()
	}
	if e != nil {
		return nil, e
	}
	var v T
	e = json.Unmarshal(b, &v)
	return &v, e
}
func list[T any](r *repo, table string) ([]*T, error) {
	rows, e := r.tx.Query(r.ctx, "SELECT data FROM "+table+" ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var v T
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}
func (r *repo) User(s string) (*id.User, error)       { return get[id.User](r, "users", s) }
func (r *repo) Users() ([]*id.User, error)            { return list[id.User](r, "users") }
func (r *repo) Group(s string) (*gr.Group, error)     { return get[gr.Group](r, "groups", s) }
func (r *repo) Groups() ([]*gr.Group, error)          { return list[gr.Group](r, "groups") }
func (r *repo) Expense(s string) (*ex.Expense, error) { return get[ex.Expense](r, "expenses", s) }
func (r *repo) Expenses() ([]*ex.Expense, error)      { return list[ex.Expense](r, "expenses") }
func (r *repo) Receipt(s string) (*re.Receipt, error) { return get[re.Receipt](r, "receipts", s) }
func (r *repo) SaveUser(v *id.User) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = r.tx.Exec(r.ctx, `INSERT INTO users(id,username,email,phone,telegram_id,oidc_subject,data) VALUES($1,NULLIF($2,''),NULLIF($3,''),NULLIF($4,''),NULLIF($5,0),NULLIF($6,''),$7) ON CONFLICT(id) DO UPDATE SET username=EXCLUDED.username,email=EXCLUDED.email,phone=EXCLUDED.phone,telegram_id=EXCLUDED.telegram_id,oidc_subject=EXCLUDED.oidc_subject,data=EXCLUDED.data`, v.ID, v.Username, v.Email, v.Phone, v.TelegramID, v.OIDCSubject, b)
	return e
}
func (r *repo) SaveGroup(v *gr.Group) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = r.tx.Exec(r.ctx, `INSERT INTO groups(id,chat_id,data) VALUES($1,NULLIF($2,0),$3) ON CONFLICT(id) DO UPDATE SET chat_id=EXCLUDED.chat_id,data=EXCLUDED.data`, v.ID, v.ChatID, b)
	return e
}
func (r *repo) SaveExpense(v *ex.Expense) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = r.tx.Exec(r.ctx, `INSERT INTO expenses(id,group_id,parent_id,created_at,data) VALUES($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data`, v.ID, v.GroupID, v.Parent, v.Audit.CreatedAt, b)
	return e
}
func (r *repo) SaveReceipt(v *re.Receipt) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = r.tx.Exec(r.ctx, `INSERT INTO receipts(id,group_id,owner_id,data) VALUES($1,NULLIF($2,'')::uuid,$3,$4) ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data`, v.ID, v.GroupID, v.Owner, b)
	return e
}
func (r *repo) PutProof(k, v string, t time.Time) error {
	_, e := r.tx.Exec(r.ctx, "DELETE FROM auth_proofs WHERE expires_at < now()")
	if e != nil {
		return e
	}
	_, e = r.tx.Exec(r.ctx, "INSERT INTO auth_proofs(key,value,expires_at) VALUES($1,$2,$3)", k, v, t)
	return e
}
func (r *repo) ConsumeProof(k string) (string, error) {
	var v string
	var t time.Time
	e := r.tx.QueryRow(r.ctx, "DELETE FROM auth_proofs WHERE key=$1 RETURNING value,expires_at", k).Scan(&v, &t)
	if errors.Is(e, pgx.ErrNoRows) || !t.After(time.Now()) {
		return "", shared.Unauthorized()
	}
	if e != nil {
		return "", fmt.Errorf("consume proof: %w", e)
	}
	return v, nil
}
