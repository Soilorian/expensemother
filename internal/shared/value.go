package shared

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const MaxMoney int64 = 9007199254740991

type Money int64

// Bound precision/exponents before allocating big integers from untrusted JSON.
func number(s string) (*big.Rat, bool) {
	if len(s) == 0 || len(s) > 40 || s[0] == '"' || !json.Valid([]byte(s)) {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil || e < -100 || e > 100 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}

func (m *Money) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] == '"' || string(b) == "null" {
		return Bad("amount must be a numeric whole-Rial value")
	}
	r, ok := number(string(b))
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return Bad("amount must be whole Rials")
	}
	n := r.Num().Int64()
	if n < 0 || n > MaxMoney {
		return Bad("amount outside supported range")
	}
	*m = Money(n)
	return nil
}
func Add(a, b Money) (Money, error) {
	r := int64(a) + int64(b)
	if r > MaxMoney || r < -MaxMoney {
		return 0, Bad("total exceeds supported range")
	}
	return Money(r), nil
}

type Decimal string

func (d *Decimal) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || b[0] == '"' || string(b) == "null" {
		return Bad("quantity/weight must be numeric")
	}
	r, ok := number(string(b))
	if !ok || r.Sign() < 0 || len(b) > 40 {
		return Bad("invalid quantity/weight")
	}
	*d = Decimal(b)
	return nil
}
func (d Decimal) MarshalJSON() ([]byte, error) {
	if d == "" {
		return []byte("1"), nil
	}
	if _, ok := number(string(d)); !ok {
		return nil, Bad("invalid decimal")
	}
	return []byte(d), nil
}
func (d Decimal) Rat() *big.Rat {
	if d == "" {
		return big.NewRat(1, 1)
	}
	r, ok := number(string(d))
	if !ok {
		return new(big.Rat)
	}
	return r
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func ValidID(s string) bool { return uuid.MatchString(s) }
func Currency(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "Rials"
	}
	if len([]rune(s)) > 32 {
		return "", Bad("currency label too long")
	}
	return s, nil
}

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
func Bad(s string) error       { return &Error{400, "INVALID_INPUT", s} }
func Conflict(s string) error  { return &Error{409, "CONFLICT", s} }
func Forbidden() error         { return &Error{403, "FORBIDDEN", "You cannot perform this action"} }
func Missing() error           { return &Error{404, "NOT_FOUND", "Resource not found"} }
func Unauthorized() error      { return &Error{401, "UNAUTHORIZED", "Sign in again"} }
func Clone[T any](v T) T       { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
