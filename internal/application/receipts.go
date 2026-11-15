package application

import (
	"context"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
)

func (s *Service) SaveReceipt(ctx context.Context, u string, v *re.Receipt) error {
	return s.Store.Within(ctx, func(r Repository) error {
		if v.Owner != u {
			return shared.Forbidden()
		}
		if v.GroupID != "" {
			g, e := r.Group(v.GroupID)
			if e != nil {
				return e
			}
			if !g.Active(u) {
				return shared.Forbidden()
			}
		}
		return r.SaveReceipt(v)
	})
}
func (s *Service) Receipt(ctx context.Context, u, id string) (out *re.Receipt, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		out, err = r.Receipt(id)
		if err != nil {
			return err
		}
		if out.Owner == u {
			return nil
		}
		if out.ExpenseID != "" {
			e, er := r.Expense(out.ExpenseID)
			if er != nil {
				return er
			}
			if canRead(r, u, e) {
				return nil
			}
		} else if out.GroupID != "" {
			g, er := r.Group(out.GroupID)
			if er != nil {
				return er
			}
			if g.Active(u) {
				return nil
			}
		}
		return shared.Missing()
	})
	return
}
