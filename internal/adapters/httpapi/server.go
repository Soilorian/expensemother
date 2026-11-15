package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"expensemother/internal/adapters/auth"
	"expensemother/internal/adapters/ocr"
	"expensemother/internal/adapters/receiptfiles"
	app "expensemother/internal/application"
	ex "expensemother/internal/expenses/domain"
	gr "expensemother/internal/groups/domain"
	id "expensemother/internal/identity/domain"
	"expensemother/internal/shared"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type endpoint func(http.ResponseWriter, *http.Request, string) (any, int, error)
type Server struct {
	Service  *app.Service
	Auth     *auth.Auth
	Files    *receiptfiles.Files
	OCR      ocr.Tesseract
	Origins  map[string]bool
	mu       sync.Mutex
	attempts map[string]attempt
}
type attempt struct {
	Count int
	Until time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.attempts = map[string]attempt{}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if e := s.Service.Store.Within(r.Context(), func(repo app.Repository) error { _, e := repo.Users(); return e }); e != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.WriteHeader(204)
	})
	for _, route := range []struct {
		Pattern string
		Public  bool
		Fn      endpoint
	}{
		{"POST /v1/auth/signup", true, s.signup}, {"POST /v1/auth/login", true, s.login}, {"GET /v1/auth/profile", false, s.profile}, {"POST /v1/auth/telegram/challenge", true, s.challenge}, {"POST /v1/auth/telegram/login", true, s.telegramLogin}, {"POST /v1/auth/telegram/mini-app", true, s.mini}, {"POST /v1/auth/telegram/link", false, s.link},
		{"GET /v1/users", false, s.users}, {"POST /v1/users", false, s.guest}, {"GET /v1/users/{userId}", false, s.user}, {"PUT /v1/users/{userId}", false, s.updateUser},
		{"GET /v1/groups", false, s.groups}, {"POST /v1/groups", false, s.saveGroup}, {"GET /v1/groups/{groupId}", false, s.group}, {"PUT /v1/groups/{groupId}", false, s.saveGroup}, {"POST /v1/groups/{groupId}/join", false, s.joinGroup}, {"GET /v1/groups/{groupId}/summary", false, s.summary},
		{"GET /v1/expenses", false, s.expenses}, {"POST /v1/expenses", false, s.saveExpense}, {"GET /v1/expenses/{expenseId}", false, s.expense}, {"PUT /v1/expenses/{expenseId}", false, s.saveExpense}, {"DELETE /v1/expenses/{expenseId}", false, s.cancel}, {"PUT /v1/expenses/{expenseId}/participation/me", false, s.participate}, {"PUT /v1/expenses/{expenseId}/items/{itemId}/claim/me", false, s.claim}, {"POST /v1/expenses/{expenseId}/payments", false, s.pay}, {"DELETE /v1/expenses/{expenseId}/payments/{paymentId}", false, s.reverse}, {"GET /v1/expenses/my-debt", false, s.debt}, {"GET /v1/expenses/my-debt/payment-info", false, s.suggestions},
		{"POST /v1/receipts", false, s.upload}, {"POST /v1/receipts/{receiptId}/extract", false, s.extract}, {"GET /v1/receipts/{receiptId}/image", false, s.image},
	} {
		mux.HandleFunc(route.Pattern, s.wrap(route.Public, route.Fn))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if origin := r.Header.Get("Origin"); origin != "" {
			if !s.Origins[origin] {
				s.fail(w, shared.Forbidden())
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) wrap(public bool, fn endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				slog.Error("request panicked", "path", r.URL.Path)
				s.fail(w, errors.New("internal error"))
			}
		}()
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		u := ""
		if public {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			s.mu.Lock()
			now := time.Now()
			v := s.attempts[host]
			if now.After(v.Until) {
				v = attempt{Until: now.Add(time.Minute)}
			}
			v.Count++
			if len(s.attempts) > 4096 {
				for k, a := range s.attempts {
					if now.After(a.Until) {
						delete(s.attempts, k)
					}
				}
			}
			s.attempts[host] = v
			s.mu.Unlock()
			if v.Count > 30 {
				s.fail(w, &shared.Error{Status: 429, Code: "RATE_LIMIT", Message: "Please try again in a minute"})
				return
			}
		} else {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				s.fail(w, shared.Unauthorized())
				return
			}
			var err error
			u, err = s.Auth.Actor(ctx, strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				s.fail(w, err)
				return
			}
		}
		for _, k := range []string{"userId", "groupId", "expenseId", "itemId", "paymentId", "receiptId"} {
			if v := r.PathValue(k); v != "" && !shared.ValidID(v) {
				s.fail(w, shared.Bad("invalid "+k))
				return
			}
		}
		body, status, err := fn(w, r, u)
		if err != nil {
			s.fail(w, err)
			return
		}
		if status == 0 {
			return
		}
		if status == 204 {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if err = json.NewEncoder(w).Encode(body); err != nil {
			slog.Warn("response encoding failed")
		}
	}
}
func (s *Server) fail(w http.ResponseWriter, err error) {
	var e *shared.Error
	if !errors.As(err, &e) {
		slog.Error("request failed", "error", err)
		e = &shared.Error{Status: 500, Code: "INTERNAL", Message: "An internal error occurred"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}
func decode(r *http.Request, v any, optional bool) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		if optional && e == io.EOF {
			return nil
		}
		return shared.Bad("invalid request body: " + e.Error())
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return shared.Bad("supply one JSON object")
	}
	return nil
}
func page[T any](r *http.Request, rows []T) (any, int, error) {
	p, n := 0, 20
	var e error
	if v := r.URL.Query().Get("page"); v != "" {
		p, e = strconv.Atoi(v)
		if e != nil || p < 0 {
			return nil, 0, shared.Bad("invalid page")
		}
	}
	if v := r.URL.Query().Get("size"); v != "" {
		n, e = strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			return nil, 0, shared.Bad("size must be 1–100")
		}
	}
	total := len(rows)
	selected := []T{}
	if p <= total/n {
		start := p * n
		end := start + n
		if end > total {
			end = total
		}
		selected = rows[start:end]
	}
	return map[string]any{"page": p, "size": n, "totalElements": total, "totalPages": (total + n - 1) / n, "elements": selected}, 200, nil
}
func groupView(g *gr.Group) any {
	members := []map[string]any{}
	for _, m := range g.Members {
		members = append(members, map[string]any{"userId": m.UserID, "isAdmin": m.Admin, "active": m.Active})
	}
	return map[string]any{"groupId": g.ID, "displayName": g.Name, "currency": g.Currency, "users": members, "profileImageId": null(g.ImageID), "requestUuid": null(g.RequestID)}
}
func null(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *Server) signup(w http.ResponseWriter, r *http.Request, _ string) (any, int, error) {
	var v struct{ Username, Password, Email string }
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	u, e := s.Service.Signup(r.Context(), v.Username, v.Password, v.Email)
	if e != nil {
		return nil, 0, e
	}
	return s.Auth.Session(u), 200, nil
}
func (s *Server) login(w http.ResponseWriter, r *http.Request, _ string) (any, int, error) {
	var v struct{ Username, Password string }
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	u, e := s.Service.Login(r.Context(), v.Username, v.Password)
	if e != nil {
		return nil, 0, e
	}
	return s.Auth.Session(u), 200, nil
}
func (s *Server) profile(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	p, e := s.Service.User(r.Context(), u)
	if e != nil {
		return nil, 0, e
	}
	return p.Profile(), 200, nil
}
func (s *Server) challenge(w http.ResponseWriter, r *http.Request, _ string) (any, int, error) {
	v, e := s.Auth.Challenge(r.Context())
	return v, 200, e
}
func (s *Server) telegramLogin(w http.ResponseWriter, r *http.Request, _ string) (any, int, error) {
	var v struct {
		ChallengeID string `json:"challengeId"`
		IDToken     string `json:"idToken"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	u, e := s.Auth.Login(r.Context(), v.ChallengeID, v.IDToken, "")
	if e != nil {
		return nil, 0, e
	}
	return s.Auth.Session(u), 200, nil
}
func (s *Server) mini(w http.ResponseWriter, r *http.Request, _ string) (any, int, error) {
	var v struct {
		InitData string `json:"initData"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	u, e := s.Auth.MiniApp(r.Context(), v.InitData, "")
	if e != nil {
		return nil, 0, e
	}
	return s.Auth.Session(u), 200, nil
}
func (s *Server) link(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		InitData    string `json:"initData"`
		ChallengeID string `json:"challengeId"`
		IDToken     string `json:"idToken"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	var p *id.User
	var e error
	if v.InitData != "" && v.IDToken == "" && v.ChallengeID == "" {
		p, e = s.Auth.MiniApp(r.Context(), v.InitData, u)
	} else if v.InitData == "" && v.IDToken != "" && v.ChallengeID != "" {
		p, e = s.Auth.Login(r.Context(), v.ChallengeID, v.IDToken, u)
	} else {
		return nil, 0, shared.Bad("supply one Telegram proof")
	}
	if e != nil {
		return nil, 0, e
	}
	return p.Profile(), 200, nil
}
func (s *Server) users(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	v, e := s.Service.Users(r.Context(), u, r.URL.Query().Get("term"))
	if e != nil {
		return nil, 0, e
	}
	return page(r, v)
}
func (s *Server) guest(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		GroupID string `json:"groupId"`
		Name    string `json:"displayName"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	p, e := s.Service.Guest(r.Context(), u, v.GroupID, v.Name)
	if e != nil {
		return nil, 0, e
	}
	return p.Public(), 201, nil
}
func (s *Server) user(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	target := r.PathValue("userId")
	if target == u {
		return s.profile(w, r, u)
	}
	users, e := s.Service.Users(r.Context(), u, "")
	if e != nil {
		return nil, 0, e
	}
	for _, p := range users {
		if p["userId"] == target {
			return p, 200, nil
		}
	}
	return nil, 0, shared.Missing()
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Name         string           `json:"displayName"`
		Username     string           `json:"username"`
		Email        string           `json:"email"`
		Phone        string           `json:"phoneNumber"`
		PaymentInfos []id.PaymentInfo `json:"paymentInfos"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	p, e := s.Service.UpdateUser(r.Context(), u, r.PathValue("userId"), &id.User{Name: v.Name, Username: v.Username, Email: v.Email, Phone: v.Phone, PaymentInfos: v.PaymentInfos})
	if e != nil {
		return nil, 0, e
	}
	if p.ID == u {
		return p.Profile(), 200, nil
	}
	return p.Public(), 200, nil
}
func (s *Server) groups(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	gs, e := s.Service.Groups(r.Context(), u)
	if e != nil {
		return nil, 0, e
	}
	v := []any{}
	term := strings.ToLower(r.URL.Query().Get("term"))
	for _, g := range gs {
		if strings.Contains(strings.ToLower(g.Name), term) {
			v = append(v, groupView(g))
		}
	}
	return page(r, v)
}
func (s *Server) group(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	g, e := s.Service.Group(r.Context(), u, r.PathValue("groupId"))
	if e != nil {
		return nil, 0, e
	}
	return groupView(g), 200, nil
}
func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Name     string `json:"displayName"`
		Currency string `json:"currency"`
		Image    string `json:"profileImageId"`
		Request  string `json:"requestUuid"`
		Users    *[]struct {
			ID     string `json:"userId"`
			Admin  bool   `json:"isAdmin"`
			Active *bool  `json:"active"`
		} `json:"users"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	if r.Method == "PUT" && v.Users == nil {
		return nil, 0, shared.Bad("users required on group replacement")
	}
	g := &gr.Group{Name: v.Name, Currency: v.Currency, ImageID: v.Image, RequestID: v.Request}
	if v.Users != nil {
		for _, m := range *v.Users {
			active := true
			if m.Active != nil {
				active = *m.Active
			}
			g.Members = append(g.Members, gr.Member{UserID: m.ID, Admin: m.Admin, Active: active})
		}
	}
	g, e := s.Service.SaveGroup(r.Context(), u, r.PathValue("groupId"), g)
	if e != nil {
		return nil, 0, e
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	return groupView(g), status, nil
}
func (s *Server) joinGroup(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	g, e := s.Service.JoinGroup(r.Context(), u, r.PathValue("groupId"))
	if e != nil {
		return nil, 0, e
	}
	return groupView(g), 200, nil
}
func (s *Server) summary(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	v, e := s.Service.Summary(r.Context(), u, r.PathValue("groupId"))
	return v, 200, e
}
func (s *Server) expenses(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	es, e := s.Service.Expenses(r.Context(), u)
	if e != nil {
		return nil, 0, e
	}
	q := r.URL.Query()
	state := q.Get("state")
	if state != "" && state != "DRAFT" && state != "FINAL" {
		return nil, 0, shared.Bad("invalid state filter")
	}
	include := q.Get("includeCancelled")
	if include != "" && include != "true" && include != "false" {
		return nil, 0, shared.Bad("invalid cancellation filter")
	}
	rows := []map[string]any{}
	for _, v := range es {
		if include != "true" && (v.CancelledAt != nil || v.ReversedAt != nil) {
			continue
		}
		if state != "" && state != v.State || q.Get("groupId") != "" && q.Get("groupId") != v.GroupID || q.Get("payerId") != "" && q.Get("payerId") != v.Payer || !strings.Contains(strings.ToLower(v.Name), strings.ToLower(q.Get("term"))) {
			continue
		}
		out, e := s.Service.ExpenseView(r.Context(), u, v.ID)
		if e != nil {
			return nil, 0, e
		}
		rows = append(rows, out)
	}
	return page(r, rows)
}
func (s *Server) expense(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	v, e := s.Service.ExpenseView(r.Context(), u, r.PathValue("expenseId"))
	return v, 200, e
}
func (s *Server) saveExpense(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	b, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if e != nil {
		return nil, 0, e
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || raw == nil {
		return nil, 0, shared.Bad("expense object required")
	}
	for _, k := range []string{"auditInfo", "cancelledAt", "cancelledByUserId", "reversedAt", "reversedByUserId"} {
		if _, ok := raw[k]; ok {
			return nil, 0, shared.Bad(k + " is server-owned")
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	var v ex.Expense
	if e = decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	p, e := s.Service.SaveExpense(r.Context(), u, r.PathValue("expenseId"), &v)
	if e != nil {
		return nil, 0, e
	}
	out, e := s.Service.ExpenseView(r.Context(), u, p.ID)
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	return out, status, e
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	return nil, 204, s.Service.Cancel(r.Context(), u, r.PathValue("expenseId"))
}
func (s *Server) participate(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Yes    *bool          `json:"participating"`
		Weight shared.Decimal `json:"share"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	if v.Yes == nil {
		return nil, 0, shared.Bad("participating required")
	}
	_, e := s.Service.MutateDraft(r.Context(), u, r.PathValue("expenseId"), func(p *ex.Expense) error { return p.Join(u, *v.Yes, v.Weight) })
	if e != nil {
		return nil, 0, e
	}
	return s.expense(w, r, u)
}
func (s *Server) claim(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Quantity *shared.Decimal `json:"quantity"`
		Weight   *shared.Decimal `json:"weight"`
	}
	if e := decode(r, &v, false); e != nil {
		return nil, 0, e
	}
	_, e := s.Service.MutateDraft(r.Context(), u, r.PathValue("expenseId"), func(p *ex.Expense) error { return p.Claim(u, r.PathValue("itemId"), v.Quantity, v.Weight) })
	if e != nil {
		return nil, 0, e
	}
	return s.expense(w, r, u)
}
func (s *Server) pay(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Debtor   string        `json:"debtorId"`
		Creditor string        `json:"creditorId"`
		Amount   *shared.Money `json:"amount"`
		Date     *time.Time    `json:"paidOn"`
		Name     string        `json:"displayName"`
	}
	if e := decode(r, &v, true); e != nil {
		return nil, 0, e
	}
	p, e := s.Service.RecordPaymentFor(r.Context(), u, r.PathValue("expenseId"), v.Debtor, v.Creditor, v.Name, v.Amount, v.Date)
	if e != nil {
		return nil, 0, e
	}
	out, e := s.Service.ExpenseView(r.Context(), u, p.ID)
	return out, 201, e
}
func (s *Server) reverse(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	return nil, 204, s.Service.Reverse(r.Context(), u, r.PathValue("expenseId"), r.PathValue("paymentId"))
}
func (s *Server) debt(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	v, e := s.Service.Debts(r.Context(), u)
	if e != nil {
		return nil, 0, e
	}
	return page(r, v)
}
func (s *Server) suggestions(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	v, e := s.Service.Suggestions(r.Context(), u)
	if e != nil {
		return nil, 0, e
	}
	return page(r, v)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, receiptfiles.Limit+(1<<20))
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		return nil, 0, shared.Bad("invalid image upload")
	}
	defer r.MultipartForm.RemoveAll()
	f, _, e := r.FormFile("image")
	if e != nil {
		return nil, 0, shared.Bad("image required")
	}
	defer f.Close()
	v, e := s.Files.Save(r.Context(), u, r.FormValue("groupId"), f)
	if e != nil {
		return nil, 0, e
	}
	return receiptfiles.Public(v), 201, nil
}
func (s *Server) extract(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	var v struct {
		Languages []string `json:"languages"`
	}
	if e := decode(r, &v, true); e != nil {
		return nil, 0, e
	}
	seen := map[string]bool{}
	for _, lang := range v.Languages {
		if lang != "fa" && lang != "en" || seen[lang] {
			return nil, 0, shared.Bad("languages must be fa/en without duplicates")
		}
		seen[lang] = true
	}
	p, e := s.Service.Receipt(r.Context(), u, r.PathValue("receiptId"))
	if e != nil {
		return nil, 0, e
	}
	return s.OCR.Extract(r.Context(), p.Path, p.ID, v.Languages), 200, nil
}
func (s *Server) image(w http.ResponseWriter, r *http.Request, u string) (any, int, error) {
	p, f, e := s.Files.Open(r.Context(), u, r.PathValue("receiptId"))
	if e != nil {
		return nil, 0, e
	}
	defer f.Close()
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "receipt", p.CreatedAt, f)
	return nil, 0, nil
}
