package domain

import "strings"

type PaymentInfo struct {
	Name       string `json:"paymentInfoName"`
	CardNumber string `json:"cardNumber"`
	Holder     string `json:"cardHolderName"`
}
type User struct {
	ID            string        `json:"userId"`
	Name          string        `json:"displayName"`
	Username      string        `json:"username"`
	Email         string        `json:"email"`
	Phone         string        `json:"phoneNumber"`
	Guest         bool          `json:"isGuest"`
	GuestGroup    string        `json:"guestGroup,omitempty"`
	PhoneVerified bool          `json:"phoneVerified"`
	EmailVerified bool          `json:"emailVerified"`
	PaymentInfos  []PaymentInfo `json:"paymentInfos"`
	PasswordHash  string        `json:"passwordHash,omitempty"`
	TelegramID    int64         `json:"telegramId,omitempty"`
	OIDCSubject   string        `json:"oidcSubject,omitempty"`
	Language      string        `json:"language,omitempty"`
}

func (u *User) Public() map[string]any {
	return map[string]any{"userId": u.ID, "displayName": u.Name, "username": u.Username, "isGuest": u.Guest}
}
func (u *User) Profile() map[string]any {
	m := u.Public()
	m["email"] = u.Email
	if u.Email == "" {
		m["email"] = nil
	}
	m["phoneNumber"] = u.Phone
	m["paymentInfos"] = u.PaymentInfos
	if u.PaymentInfos == nil {
		m["paymentInfos"] = []PaymentInfo{}
	}
	m["phoneVerified"] = u.PhoneVerified
	m["emailVerified"] = u.EmailVerified
	m["hasPassword"] = u.PasswordHash != ""
	m["telegramLinked"] = u.TelegramID != 0
	return m
}
func Normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
