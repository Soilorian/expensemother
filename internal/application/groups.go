package application

import (
	"context"
	gr "expensemother/internal/groups/domain"
	"expensemother/internal/shared"
	"time"
)

func (s *Service) Groups(ctx context.Context, u string) (out []*gr.Group, err error) {
	out = []*gr.Group{}
	err = s.Store.Within(ctx, func(r Repository) error {
		out = out[:0]
		gs, e := r.Groups()
		if e != nil {
			return e
		}
		for _, g := range gs {
			if g.Member(u) != nil {
				out = append(out, g)
			}
		}
		return nil
	})
	return
}
func (s *Service) SaveGroup(ctx context.Context, actor, path string, in *gr.Group) (out *gr.Group, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		g := shared.Clone(*in)
		now := time.Now().UTC()
		if path == "" {
			g.ID = shared.ID()
			g.ChatID = 0
			g.Join(actor)
			g.Member(actor).Admin = true
		} else {
			old, e := r.Group(path)
			if e != nil {
				return e
			}
			if !old.Admin(actor) {
				return shared.Forbidden()
			}
			g.ID = path
			g.ChatID = old.ChatID
			if g.Currency == "" {
				g.Currency = old.Currency
			}
			if g.Currency != old.Currency {
				all, e := r.Expenses()
				if e != nil {
					return e
				}
				for _, v := range all {
					if v.GroupID == path && v.State == "FINAL" {
						return shared.Conflict("currency is fixed after final expenses")
					}
				}
			}
			for _, m := range old.Members {
				n := g.Member(m.UserID)
				if n == nil {
					m.Active = false
					if m.LeftAt == nil {
						m.LeftAt = &now
					}
					g.Members = append(g.Members, m)
				} else {
					n.JoinedAt = m.JoinedAt
					if !n.Active {
						n.LeftAt = m.LeftAt
						if n.LeftAt == nil {
							n.LeftAt = &now
						}
					}
				}
			}
		}
		for i := range g.Members {
			if _, e := r.User(g.Members[i].UserID); e != nil {
				return shared.Bad("group user not found")
			}
			if g.Members[i].JoinedAt.IsZero() {
				g.Members[i].JoinedAt = now
			}
		}
		if e := g.Validate(); e != nil {
			return e
		}
		out = &g
		return r.SaveGroup(out)
	})
	return
}
func (s *Service) Setup(ctx context.Context, actor string, chat int64, name string, existing ...string) (out *gr.Group, err error) {
	u, err := s.User(ctx, actor)
	if err != nil {
		return nil, err
	}
	if s.Telegram == nil {
		return nil, shared.Forbidden()
	}
	_, admin, err := s.Telegram.Member(ctx, chat, u.TelegramID)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, shared.Forbidden()
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		gs, e := r.Groups()
		if e != nil {
			return e
		}
		for _, g := range gs {
			if g.ChatID == chat {
				if len(existing) > 0 && existing[0] != "" && existing[0] != g.ID {
					return shared.Conflict("chat is already connected")
				}
				if !g.Admin(actor) {
					return shared.Forbidden()
				}
				out = g
				return nil
			}
		}
		if len(existing) > 0 && existing[0] != "" {
			out, e = r.Group(existing[0])
			if e != nil {
				return e
			}
			if !out.Admin(actor) {
				return shared.Forbidden()
			}
			if out.ChatID != 0 {
				return shared.Conflict("group is already connected")
			}
			out.ChatID = chat
			return r.SaveGroup(out)
		}
		out = &gr.Group{ID: shared.ID(), Name: name, Currency: "Rials", ChatID: chat}
		out.Join(actor)
		out.Member(actor).Admin = true
		return r.SaveGroup(out)
	})
	return
}
func (s *Service) JoinGroup(ctx context.Context, actor, group string) (out *gr.Group, err error) {
	u, err := s.User(ctx, actor)
	if err != nil {
		return nil, err
	}
	g, err := func() (*gr.Group, error) {
		var g *gr.Group
		e := s.Store.Within(ctx, func(r Repository) error { var e error; g, e = r.Group(group); return e })
		return g, e
	}()
	if err != nil {
		return nil, err
	}
	if !g.Active(actor) {
		if s.Telegram == nil || g.ChatID == 0 || u.TelegramID == 0 {
			return nil, shared.Forbidden()
		}
		ok, _, e := s.Telegram.Member(ctx, g.ChatID, u.TelegramID)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, shared.Forbidden()
		}
	}
	err = s.Store.Within(ctx, func(r Repository) error {
		var e error
		out, e = r.Group(group)
		if e != nil {
			return e
		}
		out.Join(actor)
		return r.SaveGroup(out)
	})
	return
}
func (s *Service) ChatGroup(ctx context.Context, chat int64) (out *gr.Group, err error) {
	err = s.Store.Within(ctx, func(r Repository) error {
		gs, e := r.Groups()
		if e != nil {
			return e
		}
		for _, g := range gs {
			if g.ChatID == chat {
				out = g
				return nil
			}
		}
		return shared.Missing()
	})
	return
}
