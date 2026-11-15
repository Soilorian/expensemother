package domain

import (
	"expensemother/internal/shared"
	"strings"
	"time"
)

type Member struct {
	UserID   string     `json:"userId"`
	Admin    bool       `json:"isAdmin"`
	Active   bool       `json:"active"`
	JoinedAt time.Time  `json:"joinedAt"`
	LeftAt   *time.Time `json:"leftAt,omitempty"`
}
type Group struct {
	ID        string   `json:"groupId"`
	Name      string   `json:"displayName"`
	Currency  string   `json:"currency"`
	ImageID   string   `json:"profileImageId,omitempty"`
	RequestID string   `json:"requestUuid,omitempty"`
	Members   []Member `json:"users"`
	ChatID    int64    `json:"chatId,omitempty"`
}

func (g *Group) Member(id string) *Member {
	for i := range g.Members {
		if g.Members[i].UserID == id {
			return &g.Members[i]
		}
	}
	return nil
}
func (g *Group) Active(id string) bool { m := g.Member(id); return m != nil && m.Active }
func (g *Group) Admin(id string) bool  { m := g.Member(id); return m != nil && m.Active && m.Admin }
func (g *Group) Join(id string) {
	if m := g.Member(id); m != nil {
		m.Active = true
		m.LeftAt = nil
		return
	}
	g.Members = append(g.Members, Member{UserID: id, Active: true, JoinedAt: time.Now().UTC()})
}
func (g *Group) Validate() error {
	if strings.TrimSpace(g.Name) == "" || len([]rune(g.Name)) > 120 {
		return shared.Bad("group name required (maximum 120 characters)")
	}
	c, e := shared.Currency(g.Currency)
	if e != nil {
		return e
	}
	g.Currency = c
	seen := map[string]bool{}
	admins := 0
	for _, m := range g.Members {
		if !shared.ValidID(m.UserID) || seen[m.UserID] {
			return shared.Bad("invalid or duplicate group member")
		}
		seen[m.UserID] = true
		if m.Active && m.Admin {
			admins++
		}
	}
	if admins == 0 {
		return shared.Bad("group requires an active administrator")
	}
	return nil
}
func (g *Group) Visible(id string, created time.Time, participant bool) bool {
	m := g.Member(id)
	if m == nil {
		return false
	}
	if m.Active {
		return true
	}
	return participant && m.LeftAt != nil && !created.After(*m.LeftAt)
}
