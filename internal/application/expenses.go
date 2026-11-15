package application

import (
	"context"
	ex "expensemother/internal/expenses/domain"
	"expensemother/internal/shared"
	"sort"
	"time"
)

func (s *Service) Expenses(ctx context.Context, u string) (out []*ex.Expense, err error) {
	out = []*ex.Expense{}
	err = s.Store.Within(ctx, func(r Repository) error {
		out = out[:0]
		all, e := r.Expenses()
		if e != nil {
			return e
		}
		for _, v := range all {
			if canRead(r, u, v) {
				out = append(out, v)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Audit.CreatedAt.Equal(out[j].Audit.CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].Audit.CreatedAt.After(out[j].Audit.CreatedAt)
	})
	return
}
func (s *Service) Expense(ctx context.Context, u, id string) (out *ex.Expense, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		out, err = r.Expense(id)
		if err != nil {
			return err
		}
		if !canRead(r, u, out) {
			return shared.Missing()
		}
		return nil
	})
	return
}
func validateReferences(r Repository, actor string, e *ex.Expense) error {
	var members map[string]bool
	if e.GroupID != "" {
		g, err := r.Group(e.GroupID)
		if err != nil {
			return err
		}
		if !g.Active(actor) {
			return shared.Forbidden()
		}
		if e.Currency == "" {
			e.Currency = g.Currency
		}
		if e.Currency != g.Currency {
			return shared.Bad("currency differs from group")
		}
		members = map[string]bool{}
		for _, m := range g.Members {
			if m.Active {
				members[m.UserID] = true
			}
		}
	}
	ids := []string{e.Payer, e.Receiver}
	for _, p := range e.Shares {
		ids = append(ids, p.UserID)
	}
	for _, u := range ids {
		if u == "" {
			continue
		}
		if _, err := r.User(u); err != nil {
			return shared.Bad("participant not found")
		}
		if members != nil && !members[u] {
			return shared.Bad("participant must be an active group member")
		}
	}
	for _, id := range e.ImageIDs {
		v, err := r.Receipt(id)
		if err != nil {
			return shared.Bad("receipt not found")
		}
		if v.GroupID != e.GroupID || (v.GroupID == "" && v.Owner != actor) {
			return shared.Forbidden()
		}
		if v.ExpenseID != "" && v.ExpenseID != e.ID {
			return shared.Conflict("receipt already linked")
		}
	}
	c, err := shared.Currency(e.Currency)
	e.Currency = c
	return err
}
func (s *Service) SaveExpense(ctx context.Context, actor, path string, input *ex.Expense) (out *ex.Expense, err error) {
	if input.Type == "TRANSFER" {
		if path != "" {
			return nil, shared.Conflict("reverse and recreate repayments")
		}
		if input.State != "FINAL" || input.Amount == nil || input.PaidOn == nil {
			return nil, shared.Bad("repayment must be final")
		}
		return s.recordPayment(ctx, actor, input.Parent, input.Payer, input.Amount, input.PaidOn, input)
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		e := shared.Clone(*input)
		now := time.Now().UTC()
		if path == "" {
			if e.ID == "" {
				e.ID = shared.ID()
			}
			if _, er := r.Expense(e.ID); er == nil {
				return shared.Conflict("expense ID already exists")
			}
			e.Audit = ex.Audit{CreatedBy: actor, CreatedAt: now}
		} else {
			old, er := r.Expense(path)
			if er != nil {
				return er
			}
			if !canEdit(r, actor, old) {
				return shared.Forbidden()
			}
			if old.Type == "TRANSFER" || old.CancelledAt != nil {
				return shared.Conflict("record cannot be edited")
			}
			if e.ID != "" && e.ID != path {
				return shared.Bad("path and body IDs differ")
			}
			if e.GroupID != old.GroupID {
				return shared.Bad("expense group cannot change")
			}
			all, er := r.Expenses()
			if er != nil {
				return er
			}
			if len(activePayments(all, path)) > 0 {
				return shared.Conflict("undo repayments before editing")
			}
			e.ID = path
			e.Audit = old.Audit
		}
		e.CancelledAt = nil
		e.ReversedAt = nil
		e.CancelledBy = ""
		e.ReversedBy = ""
		if er := validateReferences(r, actor, &e); er != nil {
			return er
		}
		if er := e.Validate(e.State == "FINAL"); er != nil {
			return er
		}
		touch(&e, actor)
		if er := r.SaveExpense(&e); er != nil {
			return er
		}
		for _, id := range e.ImageIDs {
			v, er := r.Receipt(id)
			if er != nil {
				return er
			}
			v.ExpenseID = e.ID
			if er = r.SaveReceipt(v); er != nil {
				return er
			}
		}
		out = &e
		return nil
	})
	return
}
func (s *Service) MutateDraft(ctx context.Context, actor, id string, fn func(*ex.Expense) error) (out *ex.Expense, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		e, er := r.Expense(id)
		if er != nil {
			return er
		}
		if !canRead(r, actor, e) {
			return shared.Missing()
		}
		if e.GroupID != "" {
			g, er := r.Group(e.GroupID)
			if er != nil {
				return er
			}
			if !g.Active(actor) {
				return shared.Forbidden()
			}
		}
		if er = fn(e); er != nil {
			return er
		}
		if er = e.Validate(false); er != nil {
			return er
		}
		touch(e, actor)
		out = e
		return r.SaveExpense(e)
	})
	return
}
func (s *Service) Cancel(ctx context.Context, actor, id string) error {
	return s.Store.Within(ctx, func(r Repository) error {
		e, er := r.Expense(id)
		if er != nil {
			return er
		}
		if !canEdit(r, actor, e) {
			return shared.Forbidden()
		}
		if e.Type == "TRANSFER" {
			return shared.Conflict("use payment reversal")
		}
		all, er := r.Expenses()
		if er != nil {
			return er
		}
		if len(activePayments(all, id)) > 0 {
			return shared.Conflict("undo repayments first")
		}
		if e.CancelledAt == nil {
			now := time.Now().UTC()
			e.CancelledAt = &now
			e.CancelledBy = actor
		}
		return r.SaveExpense(e)
	})
}

type Debt struct {
	ExpenseID     string       `json:"expenseId"`
	Name          string       `json:"expenseName"`
	ExpenseAmount shared.Money `json:"expenseAmount"`
	GroupID       string       `json:"groupId,omitempty"`
	GroupName     string       `json:"groupName,omitempty"`
	Payer         string       `json:"payerId,omitempty"`
	Creditor      string       `json:"creditorId"`
	CreditorName  string       `json:"creditorName"`
	Debtor        string       `json:"debtorId"`
	Share         shared.Money `json:"shareAmount"`
	Paid          shared.Money `json:"paidAmount"`
	Remaining     shared.Money `json:"remainingAmount"`
	Currency      string       `json:"currency"`
}

func debts(e *ex.Expense, all []*ex.Expense) ([]Debt, error) {
	out := []Debt{}
	if !e.Active() || e.Type == "TRANSFER" {
		return out, nil
	}
	shares, err := e.Allocate(true)
	if err != nil {
		return nil, err
	}
	for u, amount := range shares {
		debtor, creditor := u, e.Payer
		if e.Type == "INCOME" {
			debtor = e.Receiver
			creditor = u
		}
		if debtor == creditor || amount == 0 {
			continue
		}
		var paid shared.Money
		for _, t := range activePayments(all, e.ID) {
			if t.Payer == debtor && t.Receiver == creditor {
				paid, err = shared.Add(paid, *t.Amount)
				if err != nil {
					return nil, err
				}
			}
		}
		out = append(out, Debt{ExpenseID: e.ID, Name: e.Name, ExpenseAmount: *e.Amount, GroupID: e.GroupID, Payer: e.Payer, Creditor: creditor, Debtor: debtor, Share: amount, Paid: paid, Remaining: amount - paid, Currency: e.Currency})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Creditor+out[i].Debtor < out[j].Creditor+out[j].Debtor })
	return out, nil
}
func (s *Service) Debts(ctx context.Context, u string) (out []Debt, err error) {
	out = []Debt{}
	err = s.Store.Within(ctx, func(r Repository) error {
		out = out[:0]
		all, e := r.Expenses()
		if e != nil {
			return e
		}
		for _, v := range all {
			if !canRead(r, u, v) {
				continue
			}
			ds, e := debts(v, all)
			if e != nil {
				return e
			}
			for _, d := range ds {
				if d.Debtor == u && d.Remaining > 0 {
					c, e := r.User(d.Creditor)
					if e != nil {
						return e
					}
					d.CreditorName = c.Name
					if d.GroupID != "" {
						g, e := r.Group(d.GroupID)
						if e != nil {
							return e
						}
						d.GroupName = g.Name
					}
					out = append(out, d)
				}
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ExpenseID+out[i].Creditor < out[j].ExpenseID+out[j].Creditor })
	return
}
func (s *Service) RecordPayment(ctx context.Context, u, parent, debtor string, amount *shared.Money, date *time.Time) (*ex.Expense, error) {
	return s.recordPayment(ctx, u, parent, debtor, amount, date, nil)
}

// Creditor disambiguates an INCOME received on behalf of multiple beneficiaries.
func (s *Service) RecordPaymentFor(ctx context.Context, u, parent, debtor, creditor, name string, amount *shared.Money, date *time.Time) (*ex.Expense, error) {
	return s.recordPayment(ctx, u, parent, debtor, amount, date, &ex.Expense{Receiver: creditor, Name: name})
}
func (s *Service) recordPayment(ctx context.Context, u, parent, debtor string, amount *shared.Money, date *time.Time, raw *ex.Expense) (out *ex.Expense, err error) {
	if debtor == "" {
		debtor = u
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		e, er := r.Expense(parent)
		if er != nil {
			return er
		}
		if !canRead(r, u, e) {
			return shared.Missing()
		}
		all, er := r.Expenses()
		if er != nil {
			return er
		}
		ds, er := debts(e, all)
		if er != nil {
			return er
		}
		choices := []Debt{}
		for _, d := range ds {
			if d.Debtor == debtor && d.Remaining > 0 && (raw == nil || raw.Receiver == "" || raw.Receiver == d.Creditor) {
				choices = append(choices, d)
			}
		}
		if len(choices) != 1 {
			return shared.Conflict("select a single outstanding creditor (for income use a TRANSFER with receiverId)")
		}
		d := choices[0]
		admin := false
		if e.GroupID != "" {
			g, er := r.Group(e.GroupID)
			if er != nil {
				return er
			}
			admin = g.Admin(u)
		}
		if u != debtor && u != d.Creditor && !admin {
			return shared.Forbidden()
		}
		n := d.Remaining
		if amount != nil {
			n = *amount
		}
		if n <= 0 || n > d.Remaining {
			return shared.Conflict("payment exceeds remaining amount or is zero")
		}
		now := time.Now().UTC()
		if date == nil {
			date = &now
		}
		out = &ex.Expense{ID: shared.ID(), GroupID: e.GroupID, Type: "TRANSFER", State: "FINAL", Payer: debtor, Receiver: d.Creditor, Amount: &n, Currency: e.Currency, PaidOn: date, Parent: e.ID, Split: "EQUAL", Audit: ex.Audit{CreatedBy: u, CreatedAt: now, UpdatedBy: u, UpdatedAt: now}}
		if raw != nil {
			if raw.ID != "" {
				out.ID = raw.ID
			}
			if (raw.GroupID != "" && raw.GroupID != e.GroupID) || (raw.Currency != "" && raw.Currency != e.Currency) || len(raw.Shares)+len(raw.Items)+len(raw.Adjustments)+len(raw.ImageIDs) > 0 {
				return shared.Bad("repayment scope/content mismatch")
			}
			out.Name = raw.Name
		}
		if _, er := r.Expense(out.ID); er == nil {
			return shared.Conflict("payment ID already exists")
		}
		if er := out.Validate(true); er != nil {
			return er
		}
		return r.SaveExpense(out)
	})
	return
}
func (s *Service) Reverse(ctx context.Context, u, parent, payment string) error {
	return s.Store.Within(ctx, func(r Repository) error {
		e, err := r.Expense(parent)
		if err != nil {
			return err
		}
		p, err := r.Expense(payment)
		if err != nil {
			return err
		}
		if p.Parent != parent || p.Type != "TRANSFER" || !canRead(r, u, e) {
			return shared.Missing()
		}
		admin := false
		if e.GroupID != "" {
			g, er := r.Group(e.GroupID)
			if er != nil {
				return er
			}
			admin = g.Admin(u)
		}
		if p.Audit.CreatedBy != u && p.Receiver != u && !admin {
			return shared.Forbidden()
		}
		if p.ReversedAt == nil {
			now := time.Now().UTC()
			p.ReversedAt = &now
			p.ReversedBy = u
		}
		return r.SaveExpense(p)
	})
}
