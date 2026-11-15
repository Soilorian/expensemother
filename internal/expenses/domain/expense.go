package domain

import (
	"expensemother/internal/shared"
	"math/big"
	"sort"
	"time"
)

type Share struct {
	UserID string         `json:"userId"`
	Weight shared.Decimal `json:"share,omitempty"`
}
type Claim struct {
	UserID   string         `json:"userId"`
	Quantity shared.Decimal `json:"quantity,omitempty"`
	Weight   shared.Decimal `json:"weight,omitempty"`
}
type Item struct {
	ID       string         `json:"itemId"`
	Name     string         `json:"displayName"`
	Quantity shared.Decimal `json:"quantity"`
	Amount   shared.Money   `json:"lineAmount"`
	Mode     string         `json:"allocationMode"`
	Claims   []Claim        `json:"claims"`
}
type Allocation struct {
	UserID string       `json:"userId"`
	Amount shared.Money `json:"amount"`
}
type Adjustment struct {
	Kind        string       `json:"kind"`
	Name        string       `json:"displayName,omitempty"`
	Amount      shared.Money `json:"amount"`
	Allocations []Allocation `json:"allocations,omitempty"`
}
type Audit struct {
	CreatedBy string    `json:"createdByUserId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedBy string    `json:"updatedByUserId"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Expense struct {
	ID          string        `json:"expenseId"`
	GroupID     string        `json:"groupId,omitempty"`
	Payer       string        `json:"payerId,omitempty"`
	Receiver    string        `json:"receiverId,omitempty"`
	Type        string        `json:"expenseType"`
	Name        string        `json:"displayName,omitempty"`
	State       string        `json:"state"`
	Amount      *shared.Money `json:"amount,omitempty"`
	Currency    string        `json:"currency"`
	PaidOn      *time.Time    `json:"paidOn,omitempty"`
	Parent      string        `json:"parentExpenseId,omitempty"`
	Split       string        `json:"splitMode"`
	Shares      []Share       `json:"sharers"`
	Items       []Item        `json:"items"`
	Adjustments []Adjustment  `json:"adjustments"`
	ImageIDs    []string      `json:"imageIds"`
	Audit       Audit         `json:"auditInfo"`
	CancelledAt *time.Time    `json:"cancelledAt,omitempty"`
	CancelledBy string        `json:"cancelledByUserId,omitempty"`
	ReversedAt  *time.Time    `json:"reversedAt,omitempty"`
	ReversedBy  string        `json:"reversedByUserId,omitempty"`
}

func (e *Expense) Active() bool {
	return e.State == "FINAL" && e.CancelledAt == nil && e.ReversedAt == nil
}
func (e *Expense) Participant(u string) bool {
	if e.Payer == u || e.Receiver == u || e.Audit.CreatedBy == u {
		return true
	}
	for _, s := range e.Shares {
		if s.UserID == u {
			return true
		}
	}
	return false
}
func (e *Expense) Editable() error {
	if e.CancelledAt != nil || e.ReversedAt != nil {
		return shared.Conflict("record is cancelled")
	}
	if e.State != "DRAFT" {
		return shared.Conflict("reopen the unpaid expense first")
	}
	return nil
}
func (e *Expense) Join(u string, yes bool, w shared.Decimal) error {
	if err := e.Editable(); err != nil {
		return err
	}
	if w.Rat().Sign() <= 0 {
		return shared.Bad("weight must be positive")
	}
	for i, s := range e.Shares {
		if s.UserID == u {
			if yes {
				e.Shares[i].Weight = w
			} else {
				e.Shares = append(e.Shares[:i], e.Shares[i+1:]...)
				for k := range e.Items {
					claims := []Claim{}
					for _, c := range e.Items[k].Claims {
						if c.UserID != u {
							claims = append(claims, c)
						}
					}
					e.Items[k].Claims = claims
				}
			}
			return nil
		}
	}
	if yes {
		e.Shares = append(e.Shares, Share{u, w})
	}
	return nil
}
func (e *Expense) Claim(u, id string, q, w *shared.Decimal) error {
	if err := e.Editable(); err != nil {
		return err
	}
	member := false
	for _, s := range e.Shares {
		if s.UserID == u {
			member = true
		}
	}
	if !member {
		return shared.Bad("join this expense before claiming items")
	}
	for i := range e.Items {
		it := &e.Items[i]
		if it.ID != id {
			continue
		}
		v := q
		if it.Mode == "SHARED" {
			v = w
		}
		if v == nil || (q != nil && w != nil) || v.Rat().Sign() < 0 {
			return shared.Bad("supply quantity for quantity items or weight for shared items")
		}
		claims := []Claim{}
		for _, c := range it.Claims {
			if c.UserID != u {
				claims = append(claims, c)
			}
		}
		if v.Rat().Sign() > 0 {
			c := Claim{UserID: u}
			if it.Mode == "SHARED" {
				c.Weight = *v
			} else {
				c.Quantity = *v
			}
			claims = append(claims, c)
		}
		previous := it.Claims
		it.Claims = claims
		_, err := e.Allocate(false)
		if err != nil {
			it.Claims = previous
		}
		return err
	}
	return shared.Missing()
}

// Split distributes the exact integer remainder deterministically, without floats.
func Split(total shared.Money, weights map[string]*big.Rat) (map[string]shared.Money, error) {
	if total < 0 || int64(total) > shared.MaxMoney {
		return nil, shared.Bad("invalid total")
	}
	sum := new(big.Rat)
	for _, w := range weights {
		if w.Sign() <= 0 {
			return nil, shared.Bad("weights must be positive")
		}
		sum.Add(sum, w)
	}
	if sum.Sign() == 0 {
		return nil, shared.Bad("choose participants")
	}
	type part struct {
		id  string
		rem *big.Rat
	}
	parts := []part{}
	out := map[string]shared.Money{}
	left := total
	for id, w := range weights {
		raw := new(big.Rat).Mul(big.NewRat(int64(total), 1), w)
		raw.Quo(raw, sum)
		floor := new(big.Int).Quo(raw.Num(), raw.Denom())
		n := shared.Money(floor.Int64())
		out[id] = n
		left -= n
		parts = append(parts, part{id, new(big.Rat).Sub(raw, new(big.Rat).SetInt(floor))})
	}
	sort.Slice(parts, func(i, j int) bool {
		c := parts[i].rem.Cmp(parts[j].rem)
		if c == 0 {
			return parts[i].id < parts[j].id
		}
		return c > 0
	})
	for i := shared.Money(0); i < left; i++ {
		out[parts[int(i)].id]++
	}
	return out, nil
}
func (e *Expense) Validate(final bool) error {
	if !shared.ValidID(e.ID) {
		return shared.Bad("invalid expense ID")
	}
	for _, id := range []string{e.GroupID, e.Payer, e.Receiver, e.Parent} {
		if id != "" && !shared.ValidID(id) {
			return shared.Bad("invalid related ID")
		}
	}
	if e.Type != "COST" && e.Type != "INCOME" && e.Type != "TRANSFER" {
		return shared.Bad("invalid expense type")
	}
	if e.State != "DRAFT" && e.State != "FINAL" {
		return shared.Bad("state must be DRAFT or FINAL")
	}
	if len([]rune(e.Name)) > 200 {
		return shared.Bad("description too long")
	}
	if e.Amount != nil && (*e.Amount < 0 || int64(*e.Amount) > shared.MaxMoney) {
		return shared.Bad("invalid amount")
	}
	if e.Split == "" {
		e.Split = "EQUAL"
	}
	if e.Split != "EQUAL" && e.Split != "WEIGHT" && e.Split != "ITEM" {
		return shared.Bad("invalid split mode")
	}
	if e.Type == "COST" && e.Receiver != "" || e.Type == "INCOME" && e.Payer != "" {
		return shared.Bad("invalid payer/receiver for type")
	}
	if e.Type == "TRANSFER" {
		if !final || e.Parent == "" || e.Payer == "" || e.Receiver == "" || e.Payer == e.Receiver || len(e.Shares)+len(e.Items)+len(e.Adjustments)+len(e.ImageIDs) > 0 {
			return shared.Bad("invalid repayment")
		}
	} else if e.Parent != "" {
		return shared.Bad("only repayments can have a parent")
	}
	if final {
		if e.Amount == nil || *e.Amount <= 0 || e.PaidOn == nil {
			return shared.Bad("final expense requires positive amount and payment date")
		}
		if e.Type == "COST" && e.Payer == "" || e.Type == "INCOME" && e.Receiver == "" {
			return shared.Bad("payer/receiver required")
		}
		if e.Type != "TRANSFER" && len(e.Shares) == 0 {
			return shared.Bad("select participants")
		}
	}
	if e.Shares == nil {
		e.Shares = []Share{}
	}
	if e.Items == nil {
		e.Items = []Item{}
	}
	if e.Adjustments == nil {
		e.Adjustments = []Adjustment{}
	}
	if e.ImageIDs == nil {
		e.ImageIDs = []string{}
	}
	if e.Type != "TRANSFER" {
		_, err := e.Allocate(final)
		return err
	}
	return nil
}
func (e *Expense) Allocate(final bool) (map[string]shared.Money, error) {
	out := map[string]shared.Money{}
	weights := map[string]*big.Rat{}
	for _, s := range e.Shares {
		if !shared.ValidID(s.UserID) || weights[s.UserID] != nil || s.Weight.Rat().Sign() <= 0 {
			return nil, shared.Bad("invalid/duplicate participant or weight")
		}
		weights[s.UserID] = s.Weight.Rat()
		if e.Split != "WEIGHT" {
			weights[s.UserID] = big.NewRat(1, 1)
		}
		out[s.UserID] = 0
	}
	if e.Split != "ITEM" {
		if len(e.Items)+len(e.Adjustments) > 0 {
			return nil, shared.Bad("items require ITEM split")
		}
		if e.Amount == nil || len(weights) == 0 {
			return out, nil
		}
		return Split(*e.Amount, weights)
	}
	if e.Type != "COST" {
		return nil, shared.Bad("items are for COST only")
	}
	seen := map[string]bool{}
	var subtotal shared.Money
	for _, it := range e.Items {
		if !shared.ValidID(it.ID) || seen[it.ID] || it.Name == "" || it.Quantity.Rat().Sign() <= 0 || it.Amount < 0 {
			return nil, shared.Bad("invalid receipt item")
		}
		seen[it.ID] = true
		if it.Mode != "QUANTITY" && it.Mode != "SHARED" {
			return nil, shared.Bad("invalid allocation mode")
		}
		var err error
		subtotal, err = shared.Add(subtotal, it.Amount)
		if err != nil {
			return nil, err
		}
		cw := map[string]*big.Rat{}
		claimed := new(big.Rat)
		for _, c := range it.Claims {
			if weights[c.UserID] == nil || cw[c.UserID] != nil {
				return nil, shared.Bad("claim requires a unique participant")
			}
			v := c.Quantity.Rat()
			if it.Mode == "SHARED" {
				if c.Quantity != "" {
					return nil, shared.Bad("shared items use weights")
				}
				v = c.Weight.Rat()
			} else if c.Weight != "" {
				return nil, shared.Bad("quantity items use quantities")
			}
			if v.Sign() <= 0 {
				return nil, shared.Bad("claim must be positive")
			}
			cw[c.UserID] = v
			claimed.Add(claimed, v)
		}
		if it.Mode == "QUANTITY" {
			cmp := claimed.Cmp(it.Quantity.Rat())
			if cmp > 0 || final && cmp != 0 {
				return nil, shared.Bad("item quantities are unassigned or overclaimed")
			}
		}
		if len(cw) == 0 {
			if final {
				return nil, shared.Bad("assign every item before finalizing")
			}
			continue
		}
		portion := it.Amount
		if !final && it.Mode == "QUANTITY" {
			r := new(big.Rat).Mul(big.NewRat(int64(it.Amount), 1), claimed)
			r.Quo(r, it.Quantity.Rat())
			portion = shared.Money(new(big.Int).Quo(r.Num(), r.Denom()).Int64())
		}
		alloc, err := Split(portion, cw)
		if err != nil {
			return nil, err
		}
		for u, v := range alloc {
			out[u], err = shared.Add(out[u], v)
			if err != nil {
				return nil, err
			}
		}
	}
	total := subtotal
	base := map[string]*big.Rat{}
	for u, v := range out {
		if v > 0 {
			base[u] = big.NewRat(int64(v), 1)
		}
	}
	if len(base) == 0 {
		base = weights
	}
	for _, a := range e.Adjustments {
		sign := shared.Money(1)
		switch a.Kind {
		case "DISCOUNT":
			sign = -1
		case "TAX", "FEE":
		default:
			return nil, shared.Bad("invalid adjustment")
		}
		var err error
		total, err = shared.Add(total, sign*a.Amount)
		if err != nil {
			return nil, err
		}
		alloc := map[string]shared.Money{}
		if len(a.Allocations) > 0 {
			var sum shared.Money
			for _, v := range a.Allocations {
				if _, ok := alloc[v.UserID]; ok || weights[v.UserID] == nil {
					return nil, shared.Bad("invalid adjustment allocation")
				}
				alloc[v.UserID] = v.Amount
				sum, err = shared.Add(sum, v.Amount)
				if err != nil {
					return nil, err
				}
			}
			if sum != a.Amount {
				return nil, shared.Bad("adjustment allocations do not match")
			}
		} else if len(base) > 0 {
			alloc, err = Split(a.Amount, base)
			if err != nil {
				return nil, err
			}
		} else if final {
			return nil, shared.Bad("assign adjustment participants")
		}
		for u, v := range alloc {
			out[u], err = shared.Add(out[u], sign*v)
			if err != nil {
				return nil, err
			}
			if out[u] < 0 {
				return nil, shared.Bad("discount exceeds allocated share")
			}
		}
	}
	if total < 0 {
		return nil, shared.Bad("discount exceeds total")
	}
	if final {
		if len(e.Items) == 0 || e.Amount == nil || total != *e.Amount {
			return nil, shared.Bad("receipt items and adjustments must equal total")
		}
		var sum shared.Money
		for _, v := range out {
			sum += v
		}
		if sum != total {
			return nil, shared.Bad("assign every charge")
		}
	}
	return out, nil
}
