package telegram

import (
	"context"
	"encoding/json"

	"expensemother/internal/adapters/ocr"
	"expensemother/internal/adapters/receiptfiles"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	"expensemother/internal/shared"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type prompt struct {
	Actor                 string
	Chat, Message         int64
	Action, Expense, Item string
	Until                 time.Time
}
type confirmation struct {
	Actor, Expense, Action, Creditor, Debtor, Payment string
	Amount                                            *shared.Money
	Chat                                              int64
	Until                                             time.Time
}
type Bot struct {
	Client  *Client
	Service *app.Service
	Files   *receiptfiles.Files
	OCR     ocr.Tesseract
	WebURL  string
	prompts map[string]prompt
	confirm map[string]confirmation
	Name    string
}

func (b *Bot) init() {
	if b.prompts == nil {
		b.prompts = map[string]prompt{}
		b.confirm = map[string]confirmation{}
	}
}
func (b *Bot) Run(ctx context.Context) error {
	b.init()
	var me struct{ Username string }
	if e := b.Client.Call(ctx, "getMe", map[string]any{}, &me); e != nil {
		return e
	}
	b.Name = me.Username
	cmds := []map[string]string{}
	for _, v := range [][2]string{{"start", "Start / شروع"}, {"setup", "Connect this group / اتصال گروه"}, {"join", "Join group / عضویت"}, {"expense", "Create draft / هزینه جدید"}, {"orders", "Orders and drafts / سفارش‌ها"}, {"mytotal", "My totals / جمع من"}, {"balances", "Group balances / مانده‌ها"}, {"receipt", "Import receipt / رسید"}, {"guest", "Add named guest / مهمان"}, {"cancel", "Cancel prompt / لغو"}, {"help", "Help / راهنما"}} {
		cmds = append(cmds, map[string]string{"command": v[0], "description": v[1]})
	}
	if e := b.Client.Call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil); e != nil {
		slog.Warn("could not register bot commands")
	}
	var offset int64
	for ctx.Err() == nil {
		var updates []Update
		e := b.Client.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 40, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("Telegram polling unavailable")
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		for _, u := range updates {
			if e = b.Handle(ctx, u); e != nil {
				slog.Warn("Telegram update failed", "update_id", u.ID, "error", e)
			}
			offset = u.ID + 1
		}
	}
	return nil
}
func key(chat int64, u string) string { return strconv.FormatInt(chat, 10) + ":" + u }
func (b *Bot) ask(ctx context.Context, chat int64, u, action, expense, item, text string) error {
	var m Message
	e := b.Client.Call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text, "reply_markup": map[string]any{"force_reply": true, "selective": true}}, &m)
	if e == nil {
		b.prompts[key(chat, u)] = prompt{Actor: u, Chat: chat, Message: m.ID, Action: action, Expense: expense, Item: item, Until: time.Now().Add(15 * time.Minute)}
	}
	return e
}
func (b *Bot) say(ctx context.Context, chat int64, text string) error {
	_, e := b.Client.Send(ctx, chat, text, nil)
	return e
}
func (b *Bot) Handle(ctx context.Context, update Update) error {
	b.init()
	now := time.Now()
	for k, p := range b.prompts {
		if now.After(p.Until) {
			delete(b.prompts, k)
		}
	}
	for k, p := range b.confirm {
		if now.After(p.Until) {
			delete(b.confirm, k)
		}
	}
	m := update.Message
	person := User{}
	if update.Callback != nil {
		if update.Callback.Message == nil {
			return nil
		}
		m = update.Callback.Message
		person = update.Callback.From
	} else if m != nil {
		person = m.From
	} else {
		return nil
	}
	if person.ID <= 0 || person.IsBot {
		return nil
	}
	u, e := b.Service.TelegramUser(ctx, person.ID, strings.TrimSpace(person.First+" "+person.Last), "", person.Language, "")
	if e != nil {
		return e
	}
	var actionErr error
	if update.Callback != nil {
		_ = b.Client.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": update.Callback.ID}, nil)
		actionErr = b.callback(ctx, u.ID, m, update.Callback.Data)
	} else {
		actionErr = b.message(ctx, u.ID, m)
	}
	if actionErr != nil {
		var text = "Could not complete that action. / انجام نشد\n"
		if er, ok := actionErr.(*shared.Error); ok {
			text += er.Message
		} else {
			text += "Please try again or check /orders before repeating a saved action."
		}
		_ = b.say(ctx, m.Chat.ID, text)
	}
	return actionErr
}
func (b *Bot) message(ctx context.Context, u string, m *Message) error {
	if p, ok := b.prompts[key(m.Chat.ID, u)]; ok && m.Reply != nil && m.Reply.ID == p.Message && !strings.HasPrefix(m.Text, "/") {
		return b.answer(ctx, u, m, p)
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return nil
	}
	cmd := strings.Split(fields[0], "@")[0]
	if strings.Contains(fields[0], "@") && b.Name != "" && !strings.EqualFold(strings.SplitN(fields[0], "@", 2)[1], b.Name) {
		return nil
	}
	arg := strings.TrimSpace(strings.TrimPrefix(m.Text, fields[0]))
	switch cmd {
	case "/start", "/help":
		rows := [][]Button{}
		if b.WebURL != "" {
			rows = append(rows, []Button{{Text: "Open app / برنامه", URL: b.WebURL}})
		}
		_, e := b.Client.Send(ctx, m.Chat.ID, "Expense Mother / مادرخرج\nAdd me as a group administrator, then /setup and /join.\n/expense Dinner 1200000 — draft in Rials\n/orders — join, edit, select items and finalize\n/receipt — Persian/English receipt photo\n/mytotal /balances /guest Name /cancel\nبرای شروع ربات را مدیر گروه کنید و /setup بزنید. مبالغ ریال هستند. رسید فارسی و انگلیسی پشتیبانی می‌شود.", rows)
		return e
	case "/cancel":
		delete(b.prompts, key(m.Chat.ID, u))
		return b.say(ctx, m.Chat.ID, "Prompt cancelled. Saved drafts remain in /orders. / لغو شد؛ پیش‌نویس ذخیره است.")
	case "/setup":
		if m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
			return shared.Bad("run /setup inside the Telegram group")
		}
		_, e := b.Service.Setup(ctx, u, m.Chat.ID, m.Chat.Title, arg)
		if e != nil {
			return e
		}
		return b.say(ctx, m.Chat.ID, "Group connected. Members can /join. / گروه متصل شد؛ اعضا /join بزنند.")
	case "/join":
		g, e := b.Service.ChatGroup(ctx, m.Chat.ID)
		if e != nil {
			return e
		}
		_, e = b.Service.JoinGroup(ctx, u, g.ID)
		if e != nil {
			return e
		}
		return b.say(ctx, m.Chat.ID, "Joined / عضو شدید")
	}
	g, e := b.Service.ChatGroup(ctx, m.Chat.ID)
	if e != nil {
		return shared.Bad("use these commands in a group after /setup")
	}
	if _, e = b.Service.JoinGroup(ctx, u, g.ID); e != nil {
		return e
	}
	switch cmd {
	case "/expense":
		if arg == "" {
			return b.ask(ctx, m.Chat.ID, u, "new", "", "", "Reply with a description and optional Rial amount, e.g. Dinner 1200000. / عنوان و مبلغ ریال")
		}
		return b.newExpense(ctx, u, m.Chat.ID, g.ID, arg)
	case "/orders":
		return b.orders(ctx, u, m.Chat.ID, g.ID, 0)
	case "/mytotal", "/balances":
		v, e := b.Service.Summary(ctx, u, g.ID)
		if e != nil {
			return e
		}
		text := fmt.Sprintf("%s · %s\nMy spending / سهم من: %d\nPayments / پرداخت‌ها: %d\nOwed / بدهکار: %d\nReceivable / طلبکار: %d\nDrafts / پیش‌نویس: %d (%d)", g.Name, g.Currency, v.MySpending, v.Payments, v.Owed, v.Receivable, v.DraftCount, v.DraftAmount)
		if cmd == "/balances" {
			for _, m := range v.Members {
				text += fmt.Sprintf("\n%s: %+d", m.Name, m.Balance)
			}
		}
		return b.say(ctx, m.Chat.ID, text)
	case "/receipt":
		return b.ask(ctx, m.Chat.ID, u, "receipt", "", "", "Reply to this message with a receipt photo (JPEG/PNG/WebP, up to 10 MiB). Persian and English supported. / تصویر رسید را پاسخ دهید.")
	case "/guest":
		if arg == "" {
			return b.ask(ctx, m.Chat.ID, u, "guest", "", "", "Guest name / نام مهمان")
		}
		p, e := b.Service.Guest(ctx, u, g.ID, arg)
		if e != nil {
			return e
		}
		return b.say(ctx, m.Chat.ID, "Guest added / مهمان اضافه شد: "+p.Name)
	}
	return nil
}
func parseAmount(s string) (shared.Money, error) {
	s = ocr.Normalize(strings.TrimSpace(s))
	if strings.Contains(s, ",") {
		parts := strings.Split(s, ",")
		if len(parts[0]) > 3 {
			return 0, shared.Bad("invalid amount separators")
		}
		for _, p := range parts[1:] {
			if len(p) != 3 {
				return 0, shared.Bad("invalid amount separators")
			}
		}
		s = strings.ReplaceAll(s, ",", "")
	}
	var n shared.Money
	if e := json.Unmarshal([]byte(s), &n); e != nil {
		return 0, e
	}
	return n, nil
}
func (b *Bot) newExpense(ctx context.Context, u string, chat int64, group, text string) error {
	parts := strings.Fields(text)
	var amount *shared.Money
	name := text
	if len(parts) > 1 {
		if n, e := parseAmount(parts[len(parts)-1]); e == nil {
			amount = &n
			name = strings.Join(parts[:len(parts)-1], " ")
		}
	}
	p, e := b.Service.SaveExpense(ctx, u, "", &ex.Expense{GroupID: group, Type: "COST", State: "DRAFT", Payer: u, Name: name, Amount: amount, Split: "EQUAL", Shares: []ex.Share{{UserID: u}}})
	if e != nil {
		return e
	}
	return b.card(ctx, u, chat, p.ID)
}
func (b *Bot) orders(ctx context.Context, u string, chat int64, group string, page int) error {
	all, e := b.Service.Expenses(ctx, u)
	if e != nil {
		return e
	}
	rows := [][]Button{}
	v := []*ex.Expense{}
	for _, p := range all {
		if p.GroupID == group && p.Type != "TRANSFER" && p.CancelledAt == nil {
			v = append(v, p)
		}
	}
	if page < 0 {
		page = 0
	}
	start := page * 8
	if start > len(v) {
		start = len(v)
	}
	for i := start; i < len(v) && i < start+8; i++ {
		p := v[i]
		rows = append(rows, []Button{{Text: p.State + " · " + short(p.Name, 50), Data: "open:" + p.ID}})
	}
	nav := []Button{}
	if page > 0 {
		nav = append(nav, Button{Text: "←", Data: fmt.Sprintf("page:%d", page-1)})
	}
	if start+8 < len(v) {
		nav = append(nav, Button{Text: "→", Data: fmt.Sprintf("page:%d", page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	_, e = b.Client.Send(ctx, chat, fmt.Sprintf("Orders / سفارش‌ها (%d)", len(v)), rows)
	return e
}
func short(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
func (b *Bot) card(ctx context.Context, u string, chat int64, id string) error {
	p, e := b.Service.Expense(ctx, u, id)
	if e != nil {
		return e
	}
	v, e := b.Service.ExpenseView(ctx, u, id)
	if e != nil {
		return e
	}
	amount := "?"
	if p.Amount != nil {
		amount = fmt.Sprint(*p.Amount)
	}
	text := fmt.Sprintf("%s\n%s · %s %s · %s\n", p.Name, p.State, amount, p.Currency, p.Split)
	shares, _ := v["sharers"].([]map[string]any)
	for _, sh := range shares {
		text += fmt.Sprintf("\n%s: %v · remaining / مانده %v", sh["userDisplayName"], sh["shareAmount"], sh["remainingAmount"])
	}
	if len(p.Items) > 0 {
		for i, it := range p.Items {
			text += fmt.Sprintf("\n%d. %s ×%s: %d", i+1, it.Name, it.Quantity, it.Amount)
		}
	}
	rows := [][]Button{{{Text: "Refresh / تازه‌سازی", Data: "open:" + id}}}
	if p.State == "DRAFT" {
		rows = append(rows, []Button{{Text: "Join / شرکت", Data: "join:" + id}, {Text: "Leave / خروج", Data: "leave:" + id}}, []Button{{Text: "Weight / وزن", Data: "weight:" + id}, {Text: "Items / اقلام", Data: "items:" + id}}, []Button{{Text: "Edit / ویرایش", Data: "edit:" + id}, {Text: "Finalize / نهایی", Data: "final:" + id}})
	} else {
		rows = append(rows, []Button{{Text: "Pay / پرداخت", Data: "pay:" + id}, {Text: "Partial / بخشی", Data: "partial:" + id}}, []Button{{Text: "Undo payment / برگشت", Data: "undo:" + id}, {Text: "Reopen / بازگشایی", Data: "reopen:" + id}})
		rows = append(rows, []Button{{Text: "Received from member / دریافت از عضو", Data: "received:" + id}})
	}
	if len(p.ImageIDs) > 0 {
		rows = append(rows, []Button{{Text: "Receipt image / تصویر رسید", Data: "receipt:" + id}})
	}
	rows = append(rows, []Button{{Text: "My share / سهم من", Data: "mine:" + id}, {Text: "Cancel expense / حذف", Data: "cancel:" + id}})
	_, e = b.Client.Send(ctx, chat, short(text, 3800), rows)
	return e
}
func (b *Bot) requestConfirm(ctx context.Context, u string, chat int64, c confirmation, text string) error {
	token := shared.ID()
	c.Actor = u
	c.Chat = chat
	c.Until = time.Now().Add(5 * time.Minute)
	b.confirm[token] = c
	_, e := b.Client.Send(ctx, chat, text, [][]Button{{{Text: "Confirm / تأیید", Data: "confirm:" + token}, {Text: "Dismiss / انصراف", Data: "dismiss:" + token}}})
	return e
}
func (b *Bot) callback(ctx context.Context, u string, m *Message, data string) error {
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return shared.Bad("invalid action")
	}
	action, id := parts[0], parts[1]
	if action == "confirm" || action == "dismiss" {
		c, ok := b.confirm[id]
		if !ok || c.Actor != u || c.Chat != m.Chat.ID || time.Now().After(c.Until) {
			return shared.Conflict("this confirmation expired or belongs to another member")
		}
		delete(b.confirm, id)
		if action == "dismiss" {
			return b.say(ctx, m.Chat.ID, "Dismissed / لغو")
		}
		switch c.Action {
		case "pay":
			var raw *ex.Expense
			var e error
			if c.Creditor != "" {
				p, er := b.Service.Expense(ctx, u, c.Expense)
				if er != nil {
					return er
				}
				now := time.Now().UTC()
				raw = &ex.Expense{GroupID: p.GroupID, Parent: p.ID, Type: "TRANSFER", State: "FINAL", Payer: c.Debtor, Receiver: c.Creditor, Amount: c.Amount, Currency: p.Currency, PaidOn: &now}
				_, e = b.Service.SaveExpense(ctx, u, "", raw)
			} else {
				_, e = b.Service.RecordPayment(ctx, u, c.Expense, c.Debtor, c.Amount, nil)
			}
			if e != nil {
				return e
			}
		case "reverse":
			if e := b.Service.Reverse(ctx, u, c.Expense, c.Payment); e != nil {
				return e
			}
		case "cancel":
			if e := b.Service.Cancel(ctx, u, c.Expense); e != nil {
				return e
			}
			return b.say(ctx, m.Chat.ID, "Cancelled / حذف شد")
		case "final":
			p, e := b.Service.Expense(ctx, u, c.Expense)
			if e != nil {
				return e
			}
			p.State = "FINAL"
			now := time.Now().UTC()
			if p.PaidOn == nil {
				p.PaidOn = &now
			}
			if _, e = b.Service.SaveExpense(ctx, u, p.ID, p); e != nil {
				return e
			}
		}
		return b.card(ctx, u, m.Chat.ID, c.Expense)
	}
	g, e := b.Service.ChatGroup(ctx, m.Chat.ID)
	if e != nil {
		return e
	}
	if _, e = b.Service.JoinGroup(ctx, u, g.ID); e != nil {
		return e
	}
	if action == "page" {
		n, _ := strconv.Atoi(id)
		return b.orders(ctx, u, m.Chat.ID, g.ID, n)
	}
	p, e := b.Service.Expense(ctx, u, id)
	if e != nil {
		return e
	}
	if p.GroupID != g.ID {
		return shared.Forbidden()
	}
	switch action {
	case "open", "mine":
		return b.card(ctx, u, m.Chat.ID, id)
	case "join", "leave":
		_, e = b.Service.MutateDraft(ctx, u, id, func(p *ex.Expense) error { return p.Join(u, action == "join", "") })
	case "weight":
		return b.ask(ctx, m.Chat.ID, u, "weight", id, "", "Reply with your positive split weight / وزن سهم")
	case "edit":
		return b.ask(ctx, m.Chat.ID, u, "edit", id, "", "Edit one field per reply:\nname Dinner\namount 1200000\npayer MEMBER_NUMBER\nparticipant MEMBER_NUMBER\ntype COST or INCOME\nreceiver MEMBER_NUMBER\nsplit EQUAL / WEIGHT / ITEM\nitem Item name | quantity | Rial line total | QUANTITY or SHARED\nline INDEX | name | quantity | Rial total | mode\ndrop INDEX\nadjust TAX/FEE/DISCOUNT amount\nclearadjust\nUse /orders to refresh.\nبرای ویرایش از دستورهای بالا استفاده کنید.\n"+b.memberList(ctx, u, g.ID))
	case "items":
		rows := [][]Button{}
		for i, it := range p.Items {
			rows = append(rows, []Button{{Text: short(it.Name, 45), Data: fmt.Sprintf("claim:%s:%d", id, i)}})
		}
		_, e = b.Client.Send(ctx, m.Chat.ID, "Select an item / انتخاب قلم", rows)
		return e
	case "claim":
		if len(parts) != 3 {
			return shared.Bad("select item")
		}
		i, _ := strconv.Atoi(parts[2])
		if i < 0 || i >= len(p.Items) {
			return shared.Missing()
		}
		it := p.Items[i]
		return b.ask(ctx, m.Chat.ID, u, "claim", id, it.ID, "Reply with quantity (QUANTITY) or weight (SHARED); 0 removes your claim. / مقدار یا وزن؛ صفر حذف\n"+it.Name+" · "+it.Mode)
	case "final":
		return b.requestConfirm(ctx, u, m.Chat.ID, confirmation{Action: "final", Expense: id}, "Finalize the current draft and its participant shares? / نهایی کردن پیش‌نویس فعلی؟")
	case "reopen":
		p.State = "DRAFT"
		_, e = b.Service.SaveExpense(ctx, u, id, p)
	case "cancel":
		return b.requestConfirm(ctx, u, m.Chat.ID, confirmation{Action: "cancel", Expense: id}, "Cancel this expense? / حذف هزینه؟")
	case "pay":
		return b.paymentChoices(ctx, u, m.Chat.ID, p, false)
	case "partial":
		return b.paymentChoices(ctx, u, m.Chat.ID, p, true)
	case "received":
		if p.Payer != u && p.Receiver != u && !g.Admin(u) {
			return shared.Forbidden()
		}
		return b.ask(ctx, m.Chat.ID, u, "received", id, "", "Reply MEMBER_NUMBER [Rial amount]. Omit amount to record their full share. / شماره عضو و مبلغ اختیاری\n"+b.memberList(ctx, u, g.ID))
	case "receipt":
		for _, rid := range p.ImageIDs {
			receipt, file, err := b.Files.Open(ctx, u, rid)
			if err != nil {
				return err
			}
			err = b.Client.SendReceipt(ctx, m.Chat.ID, file, receipt.ContentType)
			file.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case "partialto", "payto":
		if len(parts) != 3 {
			return shared.Bad("select repayment")
		}
		i, er := strconv.Atoi(parts[2])
		if er != nil {
			return er
		}
		return b.selectPayment(ctx, u, m.Chat.ID, p, i, action == "partialto")
	case "undo":
		all, er := b.Service.Expenses(ctx, u)
		if er != nil {
			return er
		}
		for _, t := range all {
			if t.Parent == id && t.Type == "TRANSFER" && t.Active() && (g.Admin(u) || t.Receiver == u || t.Audit.CreatedBy == u) {
				return b.requestConfirm(ctx, u, m.Chat.ID, confirmation{Action: "reverse", Expense: id, Payment: t.ID}, fmt.Sprintf("Undo latest visible payment of %d %s? / برگشت پرداخت؟", *t.Amount, t.Currency))
			}
		}
		return shared.Bad("no payment to undo")
	default:
		return shared.Bad("unknown action")
	}
	if e != nil {
		return e
	}
	return b.card(ctx, u, m.Chat.ID, id)
}
func (b *Bot) memberList(ctx context.Context, u, gid string) string {
	g, e := b.Service.Group(ctx, u, gid)
	if e != nil {
		return ""
	}
	text := ""
	for i, m := range g.Members {
		p, e := b.Service.User(ctx, m.UserID)
		if e == nil {
			text += fmt.Sprintf("%d. %s\n", i+1, p.Name)
		}
	}
	return text
}
func (b *Bot) paymentChoices(ctx context.Context, u string, chat int64, p *ex.Expense, partial bool) error {
	ds, e := b.Service.Debts(ctx, u)
	if e != nil {
		return e
	}
	rows := [][]Button{}
	action := "payto"
	if partial {
		action = "partialto"
	}
	i := 0
	for _, d := range ds {
		if d.ExpenseID != p.ID {
			continue
		}
		rows = append(rows, []Button{{Text: fmt.Sprintf("%s: %d %s", d.CreditorName, d.Remaining, d.Currency), Data: fmt.Sprintf("%s:%s:%d", action, p.ID, i)}})
		i++
	}
	if len(rows) == 0 {
		return shared.Bad("you have no outstanding share here")
	}
	_, e = b.Client.Send(ctx, chat, "Choose creditor / انتخاب طلبکار", rows)
	return e
}
func (b *Bot) selectPayment(ctx context.Context, u string, chat int64, p *ex.Expense, i int, partial bool) error {
	ds, e := b.Service.Debts(ctx, u)
	if e != nil {
		return e
	}
	selected := []app.Debt{}
	for _, d := range ds {
		if d.ExpenseID == p.ID {
			selected = append(selected, d)
		}
	}
	if i < 0 || i >= len(selected) {
		return shared.Conflict("refresh the payment list")
	}
	d := selected[i]
	if partial {
		return b.ask(ctx, chat, u, "partial", p.ID, d.Creditor, fmt.Sprintf("Partial payment to %s, at most %d Rials / مبلغ پرداخت", d.CreditorName, d.Remaining))
	}
	return b.requestConfirm(ctx, u, chat, confirmation{Action: "pay", Expense: p.ID, Creditor: d.Creditor, Debtor: u, Amount: &d.Remaining}, fmt.Sprintf("Record payment of %d %s to %s? / ثبت پرداخت؟", d.Remaining, d.Currency, d.CreditorName))
}
