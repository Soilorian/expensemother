package application

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"expensemother/internal/shared"
)

var bytesReader = bytes.NewReader

func (s *Service) ExpenseView(ctx context.Context, u, id string) (out map[string]any, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		e, er := r.Expense(id)
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
		b, er := json.Marshal(e)
		if er != nil {
			return er
		}
		dec := json.NewDecoder(bytesReader(b))
		dec.UseNumber()
		if er = dec.Decode(&out); er != nil {
			return er
		}
		shares, er := e.Allocate(e.Active())
		if e.Type == "TRANSFER" {
			shares = map[string]shared.Money{}
			er = nil
		}
		if er != nil {
			return er
		}
		ds, er := debts(e, all)
		if er != nil {
			return er
		}
		view := []map[string]any{}
		for _, sh := range e.Shares {
			p, er := r.User(sh.UserID)
			if er != nil {
				return er
			}
			amount := shares[sh.UserID]
			paid := shared.Money(0)
			remaining := amount
			if e.Type == "COST" && sh.UserID == e.Payer {
				paid = amount
				remaining = 0
			}
			if e.Type == "INCOME" && sh.UserID == e.Receiver {
				paid = amount
				remaining = 0
			}
			for _, d := range ds {
				if e.Type == "COST" && d.Debtor == sh.UserID || e.Type == "INCOME" && d.Creditor == sh.UserID {
					paid = d.Paid
					remaining = d.Remaining
				}
			}
			view = append(view, map[string]any{"userId": sh.UserID, "share": sh.Weight, "userDisplayName": p.Name, "shareAmount": amount, "paidAmount": paid, "remainingAmount": remaining, "isPayed": e.Active() && remaining == 0})
		}
		out["sharers"] = view
		for key, user := range map[string]string{"payerDisplayName": e.Payer, "receiverDisplayName": e.Receiver} {
			if user != "" {
				p, er := r.User(user)
				if er != nil {
					return er
				}
				out[key] = p.Name
			}
		}
		if e.GroupID != "" {
			g, er := r.Group(e.GroupID)
			if er != nil {
				return er
			}
			out["groupDisplayName"] = g.Name
		}
		return nil
	})
	return
}

type MemberBalance struct {
	UserID     string       `json:"userId"`
	Name       string       `json:"displayName"`
	Balance    shared.Money `json:"balance"`
	Owed       shared.Money `json:"amountOwed"`
	Receivable shared.Money `json:"amountReceivable"`
}
type Summary struct {
	GroupID     string           `json:"groupId"`
	Currency    string           `json:"currency"`
	Spending    shared.Money     `json:"totalSpending"`
	DraftCount  int              `json:"draftCount"`
	DraftAmount shared.Money     `json:"draftPreviewAmount"`
	MySpending  shared.Money     `json:"mySpending"`
	Payments    shared.Money     `json:"myPaymentsMade"`
	Owed        shared.Money     `json:"myAmountOwed"`
	Receivable  shared.Money     `json:"myAmountReceivable"`
	Balance     shared.Money     `json:"myBalance"`
	Members     []*MemberBalance `json:"members"`
}

func (s *Service) Summary(ctx context.Context, u, group string) (out Summary, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		g, e := r.Group(group)
		if e != nil {
			return e
		}
		if g.Member(u) == nil {
			return shared.Missing()
		}
		out = Summary{GroupID: g.ID, Currency: g.Currency, Members: []*MemberBalance{}}
		all, e := r.Expenses()
		if e != nil {
			return e
		}
		bm := map[string]*MemberBalance{}
		for _, m := range g.Members {
			p, e := r.User(m.UserID)
			if e != nil {
				return e
			}
			bm[m.UserID] = &MemberBalance{UserID: m.UserID, Name: p.Name}
		}
		add := func(p *shared.Money, v shared.Money) error { n, e := shared.Add(*p, v); *p = n; return e }
		for _, v := range all {
			if v.GroupID != group || !canRead(r, u, v) || v.CancelledAt != nil || v.ReversedAt != nil {
				continue
			}
			if v.State == "DRAFT" {
				out.DraftCount++
				if v.Type == "COST" && v.Amount != nil {
					if e = add(&out.DraftAmount, *v.Amount); e != nil {
						return e
					}
				}
				continue
			}
			if v.Type == "TRANSFER" {
				if v.Payer == u {
					if e = add(&out.Payments, *v.Amount); e != nil {
						return e
					}
				}
				continue
			}
			shares, e := v.Allocate(true)
			if e != nil {
				return e
			}
			if v.Type == "COST" {
				if e = add(&out.Spending, *v.Amount); e != nil {
					return e
				}
				if e = add(&out.MySpending, shares[u]); e != nil {
					return e
				}
			}
			ds, e := debts(v, all)
			if e != nil {
				return e
			}
			for _, d := range ds {
				if bm[d.Debtor] == nil || bm[d.Creditor] == nil {
					return shared.Bad("missing historical membership")
				}
				if e = add(&bm[d.Debtor].Owed, d.Remaining); e != nil {
					return e
				}
				if e = add(&bm[d.Creditor].Receivable, d.Remaining); e != nil {
					return e
				}
			}
		}
		for id, b := range bm {
			b.Balance = b.Receivable - b.Owed
			if g.Active(u) || id == u {
				out.Members = append(out.Members, b)
			}
		}
		sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].UserID < out.Members[j].UserID })
		if b := bm[u]; b != nil {
			out.Owed = b.Owed
			out.Receivable = b.Receivable
			out.Balance = b.Balance
		}
		return nil
	})
	return
}

type Suggestion struct {
	Payee    string       `json:"payeeId"`
	Name     string       `json:"payeeName"`
	Payer    string       `json:"payerId"`
	Amount   shared.Money `json:"amount"`
	GroupID  string       `json:"groupId,omitempty"`
	Currency string       `json:"currency"`
	Expenses []Origin     `json:"expenses"`
}
type Origin struct {
	ID     string       `json:"expenseId"`
	Amount shared.Money `json:"amount"`
}

func (s *Service) Suggestions(ctx context.Context, u string) ([]*Suggestion, error) {
	ds, err := s.Debts(ctx, u)
	if err != nil {
		return nil, err
	}
	m := map[string]*Suggestion{}
	out := []*Suggestion{}
	for _, d := range ds {
		k := d.Creditor + "/" + d.GroupID + "/" + d.Currency
		p := m[k]
		if p == nil {
			p = &Suggestion{Payee: d.Creditor, Name: d.CreditorName, Payer: u, GroupID: d.GroupID, Currency: d.Currency, Expenses: []Origin{}}
			m[k] = p
			out = append(out, p)
		}
		p.Amount, err = shared.Add(p.Amount, d.Remaining)
		if err != nil {
			return nil, err
		}
		p.Expenses = append(p.Expenses, Origin{d.ExpenseID, d.Remaining})
	}
	return out, nil
}
