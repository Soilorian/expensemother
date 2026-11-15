// Package memory is a transactional test adapter, never the production default.
package memory

import (
	"context"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	id "expensemother/internal/identity/domain"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
	"sync"
	"time"
)

type proof struct {
	Value   string
	Expires time.Time
}
type repo struct {
	U map[string]*id.User
	G map[string]*gr.Group
	E map[string]*ex.Expense
	R map[string]*re.Receipt
	P map[string]proof
}
type Store struct {
	mu sync.Mutex
	r  repo
}

func New() *Store {
	return &Store{r: repo{map[string]*id.User{}, map[string]*gr.Group{}, map[string]*ex.Expense{}, map[string]*re.Receipt{}, map[string]proof{}}}
}
func (s *Store) Within(ctx context.Context, fn func(app.Repository) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	r := shared.Clone(s.r)
	if e := fn(&r); e != nil {
		return e
	}
	s.r = r
	return nil
}
func (r *repo) User(s string) (*id.User, error) {
	v, ok := r.U[s]
	if !ok {
		return nil, shared.Missing()
	}
	return v, nil
}
func (r *repo) Group(s string) (*gr.Group, error) {
	v, ok := r.G[s]
	if !ok {
		return nil, shared.Missing()
	}
	return v, nil
}
func (r *repo) Expense(s string) (*ex.Expense, error) {
	v, ok := r.E[s]
	if !ok {
		return nil, shared.Missing()
	}
	return v, nil
}
func (r *repo) Receipt(s string) (*re.Receipt, error) {
	v, ok := r.R[s]
	if !ok {
		return nil, shared.Missing()
	}
	return v, nil
}
func (r *repo) Users() ([]*id.User, error) {
	o := []*id.User{}
	for _, v := range r.U {
		o = append(o, v)
	}
	return o, nil
}
func (r *repo) Groups() ([]*gr.Group, error) {
	o := []*gr.Group{}
	for _, v := range r.G {
		o = append(o, v)
	}
	return o, nil
}
func (r *repo) Expenses() ([]*ex.Expense, error) {
	o := []*ex.Expense{}
	for _, v := range r.E {
		o = append(o, v)
	}
	return o, nil
}
func (r *repo) SaveUser(v *id.User) error       { r.U[v.ID] = v; return nil }
func (r *repo) SaveGroup(v *gr.Group) error     { r.G[v.ID] = v; return nil }
func (r *repo) SaveExpense(v *ex.Expense) error { r.E[v.ID] = v; return nil }
func (r *repo) SaveReceipt(v *re.Receipt) error { r.R[v.ID] = v; return nil }
func (r *repo) PutProof(k, v string, t time.Time) error {
	if old, ok := r.P[k]; ok && old.Expires.After(time.Now()) {
		return shared.Unauthorized()
	}
	r.P[k] = proof{v, t}
	return nil
}
func (r *repo) ConsumeProof(k string) (string, error) {
	v, ok := r.P[k]
	if !ok || !v.Expires.After(time.Now()) {
		return "", shared.Unauthorized()
	}
	delete(r.P, k)
	return v.Value, nil
}
