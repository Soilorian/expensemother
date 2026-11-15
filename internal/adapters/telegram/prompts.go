package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"expensemother/internal/adapters/ocr"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	"expensemother/internal/shared"
	"fmt"
	"strconv"
	"strings"
)

func (b *Bot) answer(ctx context.Context, u string, m *Message, q prompt) error {
	g, e := b.Service.ChatGroup(ctx, m.Chat.ID)
	if e != nil {
		return e
	}
	if _, e = b.Service.JoinGroup(ctx, u, g.ID); e != nil {
		return e
	}
	text := strings.TrimSpace(ocr.Normalize(m.Text))
	switch q.Action {
	case "new":
		if text == "" {
			return shared.Bad("enter a name")
		}
		if e = b.newExpense(ctx, u, m.Chat.ID, g.ID, text); e != nil {
			return e
		}
	case "guest":
		if _, e = b.Service.Guest(ctx, u, g.ID, text); e != nil {
			return e
		}
		_ = b.say(ctx, m.Chat.ID, "Guest added / اضافه شد")
	case "receipt":
		file := ""
		if len(m.Photos) > 0 {
			file = m.Photos[len(m.Photos)-1].ID
		} else if m.Document != nil {
			file = m.Document.ID
		}
		if file == "" {
			return shared.Bad("reply with a receipt image")
		}
		data, e := b.Client.Download(ctx, file)
		if e != nil {
			return e
		}
		r, e := b.Files.Save(ctx, u, g.ID, bytes.NewReader(data))
		if e != nil {
			return e
		}
		_ = b.say(ctx, m.Chat.ID, "Reading receipt / در حال خواندن رسید…")
		x := b.OCR.Extract(ctx, r.Path, r.ID, []string{"fa", "en"})
		p := &ex.Expense{GroupID: g.ID, Type: "COST", State: "DRAFT", Payer: u, Name: "Receipt / رسید", Amount: x.Amount, Split: "ITEM", Shares: []ex.Share{{UserID: u}}, Items: x.Items, Adjustments: x.Adjustments, ImageIDs: []string{r.ID}}
		if x.DetectedUnit == "Toman" {
			p.Amount = nil
			p.Items = []ex.Item{}
			p.Adjustments = []ex.Adjustment{}
			x.Warnings = append(x.Warnings, "Add the converted Rial items and total manually using Edit.")
		}
		p, e = b.Service.SaveExpense(ctx, u, "", p)
		if e != nil {
			return e
		}
		_ = b.say(ctx, m.Chat.ID, strings.Join(x.Warnings, "\n"))
		if e = b.card(ctx, u, m.Chat.ID, p.ID); e != nil {
			return e
		}
	default:
		p, e := b.Service.Expense(ctx, u, q.Expense)
		if e != nil {
			return e
		}
		if p.GroupID != g.ID {
			return shared.Forbidden()
		}
		switch q.Action {
		case "weight":
			var w shared.Decimal
			if json.Unmarshal([]byte(text), &w) != nil || w.Rat().Sign() <= 0 {
				return shared.Bad("enter a positive numeric weight")
			}
			_, e = b.Service.MutateDraft(ctx, u, p.ID, func(p *ex.Expense) error { return p.Join(u, true, w) })
		case "claim":
			var v shared.Decimal
			if json.Unmarshal([]byte(text), &v) != nil {
				return shared.Bad("enter a nonnegative number")
			}
			_, e = b.Service.MutateDraft(ctx, u, p.ID, func(p *ex.Expense) error {
				for _, it := range p.Items {
					if it.ID == q.Item {
						if it.Mode == "SHARED" {
							return p.Claim(u, it.ID, nil, &v)
						}
						return p.Claim(u, it.ID, &v, nil)
					}
				}
				return shared.Missing()
			})
		case "partial":
			n, er := parseAmount(text)
			if er != nil {
				return er
			}
			ds, er := b.Service.Debts(ctx, u)
			if er != nil {
				return er
			}
			var debt *app.Debt
			for i := range ds {
				if ds[i].ExpenseID == p.ID && ds[i].Creditor == q.Item {
					debt = &ds[i]
				}
			}
			if debt == nil || n <= 0 || n > debt.Remaining {
				return shared.Bad("payment exceeds remaining debt")
			}
			e = b.requestConfirm(ctx, u, m.Chat.ID, confirmation{Action: "pay", Expense: p.ID, Creditor: debt.Creditor, Debtor: u, Amount: &n}, fmt.Sprintf("Record %d Rials paid to %s? / ثبت پرداخت؟", n, debt.CreditorName))
		case "edit":
			e = b.edit(ctx, u, p, text)
		case "received":
			parts := strings.Fields(text)
			if len(parts) < 1 || len(parts) > 2 {
				return shared.Bad("reply MEMBER_NUMBER [amount]")
			}
			i, er := strconv.Atoi(parts[0])
			if er != nil || i < 1 || i > len(g.Members) {
				return shared.Bad("invalid member number")
			}
			var n *shared.Money
			if len(parts) == 2 {
				v, er := parseAmount(parts[1])
				if er != nil {
					return er
				}
				n = &v
			}
			e = b.requestConfirm(ctx, u, m.Chat.ID, confirmation{Action: "pay", Expense: p.ID, Debtor: g.Members[i-1].UserID, Amount: n}, "Record received payment for this member? / ثبت دریافت از عضو؟")
		default:
			return shared.Bad("unknown prompt")
		}
		if e != nil {
			return e
		}
		if q.Action != "partial" && q.Action != "received" {
			if e = b.card(ctx, u, m.Chat.ID, p.ID); e != nil {
				return e
			}
		}
	}
	delete(b.prompts, key(m.Chat.ID, u))
	return nil
}
func (b *Bot) edit(ctx context.Context, u string, p *ex.Expense, text string) error {
	if p.State != "DRAFT" {
		return shared.Conflict("reopen the unpaid expense before editing")
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return shared.Bad("enter an edit command")
	}
	cmd := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	switch cmd {
	case "name":
		p.Name = arg
	case "amount":
		n, e := parseAmount(arg)
		if e != nil {
			return e
		}
		p.Amount = &n
	case "payer", "receiver", "participant":
		i, e := strconv.Atoi(arg)
		g, er := b.Service.Group(ctx, u, p.GroupID)
		if e != nil || er != nil || i < 1 || i > len(g.Members) {
			return shared.Bad("invalid member number")
		}
		member := g.Members[i-1].UserID
		if cmd == "payer" {
			p.Payer = member
		} else if cmd == "receiver" {
			p.Receiver = member
		} else {
			if e = p.Join(member, true, ""); e != nil {
				return e
			}
		}
	case "type":
		p.Type = strings.ToUpper(arg)
		if p.Type == "INCOME" {
			p.Payer = ""
			p.Receiver = u
			p.Split = "EQUAL"
			p.Items = []ex.Item{}
			p.Adjustments = []ex.Adjustment{}
		} else if p.Type == "COST" {
			p.Receiver = ""
			p.Payer = u
		} else {
			return shared.Bad("use COST or INCOME")
		}
	case "split":
		p.Split = strings.ToUpper(arg)
		if p.Split != "ITEM" {
			p.Items = []ex.Item{}
			p.Adjustments = []ex.Adjustment{}
		}
	case "item", "line":
		parts := strings.Split(arg, "|")
		index := -1
		if cmd == "line" {
			if len(parts) != 5 {
				return shared.Bad("line INDEX | name | quantity | total | mode")
			}
			i, e := strconv.Atoi(strings.TrimSpace(parts[0]))
			if e != nil || i < 1 || i > len(p.Items) {
				return shared.Bad("invalid line index")
			}
			index = i - 1
			parts = parts[1:]
		}
		if len(parts) != 4 {
			return shared.Bad("item Name | quantity | Rial total | QUANTITY/SHARED")
		}
		var q shared.Decimal
		if json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &q) != nil || q.Rat().Sign() <= 0 {
			return shared.Bad("invalid quantity")
		}
		n, e := parseAmount(parts[2])
		if e != nil {
			return e
		}
		it := ex.Item{ID: shared.ID(), Name: strings.TrimSpace(parts[0]), Quantity: q, Amount: n, Mode: strings.ToUpper(strings.TrimSpace(parts[3])), Claims: []ex.Claim{}}
		if index >= 0 {
			it.ID = p.Items[index].ID
			it.Claims = p.Items[index].Claims
			p.Items[index] = it
		} else {
			p.Items = append(p.Items, it)
		}
		p.Split = "ITEM"
	case "drop":
		i, e := strconv.Atoi(arg)
		if e != nil || i < 1 || i > len(p.Items) {
			return shared.Bad("invalid line index")
		}
		p.Items = append(p.Items[:i-1], p.Items[i:]...)
	case "adjust":
		v := strings.Fields(arg)
		if len(v) != 2 {
			return shared.Bad("adjust TAX/FEE/DISCOUNT amount")
		}
		n, e := parseAmount(v[1])
		if e != nil {
			return e
		}
		p.Adjustments = append(p.Adjustments, ex.Adjustment{Kind: strings.ToUpper(v[0]), Amount: n})
	case "clearadjust":
		p.Adjustments = []ex.Adjustment{}
	default:
		return shared.Bad("unknown edit command")
	}
	_, e := b.Service.SaveExpense(ctx, u, p.ID, p)
	return e
}
