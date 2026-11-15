package application

import (
	"context"
	id "expensemother/internal/identity/domain"
	"expensemother/internal/shared"
	"golang.org/x/crypto/bcrypt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

func (s *Service) Signup(ctx context.Context, username, password, email string) (out *id.User, err error) {
	username = id.Normalize(username)
	email = id.Normalize(email)
	if utf8.RuneCountInString(username) < 3 || utf8.RuneCountInString(username) > 64 || utf8.RuneCountInString(password) < 8 || len(password) > 72 {
		return nil, shared.Bad("username 3–64 characters; password at least 8 characters and at most 72 UTF-8 bytes")
	}
	if email != "" {
		if a, e := mail.ParseAddress(email); e != nil || a.Address != email {
			return nil, shared.Bad("invalid email")
		}
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return nil, e
	}
	out = &id.User{ID: shared.ID(), Name: username, Username: username, Email: email, PasswordHash: string(hash), PaymentInfos: []id.PaymentInfo{}}
	err = s.Store.Within(ctx, func(r Repository) error {
		users, e := r.Users()
		if e != nil {
			return e
		}
		for _, u := range users {
			if u.Username == username || (email != "" && u.Email == email) {
				return shared.Conflict("username or email already exists")
			}
		}
		return r.SaveUser(out)
	})
	return
}
func (s *Service) Login(ctx context.Context, username, password string) (out *id.User, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		users, e := r.Users()
		if e != nil {
			return e
		}
		for _, u := range users {
			if u.Username == id.Normalize(username) && !u.Guest && bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil {
				out = u
				return nil
			}
		}
		return shared.Unauthorized()
	})
	return
}
func (s *Service) TelegramUser(ctx context.Context, tg int64, name, subject, lang, link string) (out *id.User, err error) {
	if tg <= 0 {
		return nil, shared.Unauthorized()
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		users, e := r.Users()
		if e != nil {
			return e
		}
		for _, u := range users {
			if subject != "" && u.OIDCSubject == subject && u.TelegramID != tg {
				return shared.Conflict("inconsistent Telegram identity")
			}
			if u.TelegramID == tg {
				if link != "" && u.ID != link {
					return shared.Conflict("Telegram belongs to another account")
				}
				if subject != "" && u.OIDCSubject != "" && u.OIDCSubject != subject {
					return shared.Conflict("inconsistent Telegram subject")
				}
				if subject != "" {
					u.OIDCSubject = subject
				}
				out = u
				return r.SaveUser(u)
			}
		}
		if link != "" {
			out, e = r.User(link)
			if e != nil {
				return e
			}
			if out.Guest || out.TelegramID != 0 {
				return shared.Conflict("account already linked")
			}
		} else {
			if strings.TrimSpace(name) == "" {
				name = "Telegram member"
			}
			out = &id.User{ID: shared.ID(), Name: name, PaymentInfos: []id.PaymentInfo{}}
		}
		out.TelegramID = tg
		out.OIDCSubject = subject
		out.Language = lang
		return r.SaveUser(out)
	})
	return
}
func (s *Service) Guest(ctx context.Context, actor, group, name string) (out *id.User, err error) {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 120 {
		return nil, shared.Bad("guest name required")
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		g, e := r.Group(group)
		if e != nil {
			return e
		}
		if !g.Admin(actor) {
			return shared.Forbidden()
		}
		out = &id.User{ID: shared.ID(), Name: name, Guest: true, GuestGroup: group, PaymentInfos: []id.PaymentInfo{}}
		if e = r.SaveUser(out); e != nil {
			return e
		}
		g.Join(out.ID)
		return r.SaveGroup(g)
	})
	return
}
func (s *Service) UpdateUser(ctx context.Context, actor, target string, patch *id.User) (out *id.User, err error) {
	if strings.TrimSpace(patch.Name) == "" || utf8.RuneCountInString(patch.Name) > 120 {
		return nil, shared.Bad("display name required (maximum 120)")
	}
	if utf8.RuneCountInString(patch.Username) > 64 {
		return nil, shared.Bad("username too long")
	}
	for _, p := range patch.PaymentInfos {
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Holder) == "" || strings.TrimSpace(p.CardNumber) == "" || len(p.CardNumber) > 32 {
			return nil, shared.Bad("payment instructions require a name, holder and card number (up to 32 characters)")
		}
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		u, e := r.User(target)
		if e != nil {
			return e
		}
		if actor != target {
			g, e := r.Group(u.GuestGroup)
			if !u.Guest || e != nil || !g.Admin(actor) {
				return shared.Forbidden()
			}
			u.Name = patch.Name
			out = u
			return r.SaveUser(u)
		}
		patch.Email = id.Normalize(patch.Email)
		patch.Username = id.Normalize(patch.Username)
		if patch.Email != "" {
			if a, e := mail.ParseAddress(patch.Email); e != nil || a.Address != patch.Email {
				return shared.Bad("invalid email")
			}
		}
		users, e := r.Users()
		if e != nil {
			return e
		}
		for _, other := range users {
			if other.ID != target && ((patch.Email != "" && patch.Email == other.Email) || (patch.Username != "" && patch.Username == other.Username) || (patch.Phone != "" && patch.Phone == other.Phone)) {
				return shared.Conflict("contact or username already exists")
			}
		}
		if patch.Email != u.Email {
			u.EmailVerified = false
		}
		if patch.Phone != u.Phone {
			u.PhoneVerified = false
		}
		u.Email = patch.Email
		u.Phone = patch.Phone
		u.Username = patch.Username
		u.Name = patch.Name
		u.PaymentInfos = patch.PaymentInfos
		out = u
		return r.SaveUser(u)
	})
	return
}
func (s *Service) Users(ctx context.Context, actor, term string) (out []map[string]any, err error) {
	out = []map[string]any{}
	err = s.Store.Within(ctx, func(r Repository) error {
		out = out[:0]
		groups, e := r.Groups()
		if e != nil {
			return e
		}
		visible := map[string]bool{actor: true}
		for _, g := range groups {
			if g.Active(actor) {
				for _, m := range g.Members {
					visible[m.UserID] = true
				}
			}
		}
		users, e := r.Users()
		if e != nil {
			return e
		}
		term = id.Normalize(term)
		for _, u := range users {
			if visible[u.ID] && strings.Contains(id.Normalize(u.Name+" "+u.Username), term) {
				out = append(out, u.Public())
			}
		}
		return nil
	})
	return
}
