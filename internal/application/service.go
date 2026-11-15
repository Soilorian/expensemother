package application

import (
	"context"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	id "expensemother/internal/identity/domain"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
	"time"
)

// Repository methods correspond to aggregate persistence, not transport DTOs.
type Repository interface {
	User(string) (*id.User, error)
	Users() ([]*id.User, error)
	SaveUser(*id.User) error
	Group(string) (*gr.Group, error)
	Groups() ([]*gr.Group, error)
	SaveGroup(*gr.Group) error
	Expense(string) (*ex.Expense, error)
	Expenses() ([]*ex.Expense, error)
	SaveExpense(*ex.Expense) error
	Receipt(string) (*re.Receipt, error)
	SaveReceipt(*re.Receipt) error
	PutProof(string, string, time.Time) error
	ConsumeProof(string) (string, error)
}
type Store interface {
	Within(context.Context, func(Repository) error) error
}
type TelegramMembership interface {
	Member(context.Context, int64, int64) (bool, bool, error)
}
type Service struct {
	Store    Store
	Telegram TelegramMembership
}

func New(st Store) *Service { return &Service{Store: st} }
func (s *Service) User(ctx context.Context, u string) (out *id.User, err error) {
	err = s.Store.Within(ctx, func(r Repository) error { out, err = r.User(u); return err })
	return
}
func (s *Service) Group(ctx context.Context, u, g string) (out *gr.Group, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		out, err = r.Group(g)
		if err != nil {
			return err
		}
		if out.Member(u) == nil {
			return shared.Missing()
		}
		return nil
	})
	return
}
func canRead(r Repository, u string, e *ex.Expense) bool {
	if e.GroupID == "" {
		return e.Participant(u)
	}
	g, err := r.Group(e.GroupID)
	return err == nil && g.Visible(u, e.Audit.CreatedAt, e.Participant(u))
}
func canEdit(r Repository, u string, e *ex.Expense) bool {
	if !canRead(r, u, e) {
		return false
	}
	if e.GroupID != "" {
		g, err := r.Group(e.GroupID)
		if err != nil || !g.Active(u) {
			return false
		}
		if g.Admin(u) {
			return true
		}
	}
	return e.Audit.CreatedBy == u
}
func activePayments(all []*ex.Expense, parent string) []*ex.Expense {
	out := []*ex.Expense{}
	for _, v := range all {
		if v.Type == "TRANSFER" && v.Parent == parent && v.Active() {
			out = append(out, v)
		}
	}
	return out
}
func touch(e *ex.Expense, u string) { e.Audit.UpdatedBy = u; e.Audit.UpdatedAt = time.Now().UTC() }
