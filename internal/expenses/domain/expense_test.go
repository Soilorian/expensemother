package domain

import (
	"encoding/json"
	"expensemother/internal/shared"
	"math/big"
	"testing"
	"time"
)

const a = "10000000-0000-4000-8000-000000000001"
const b = "10000000-0000-4000-8000-000000000002"
const c = "10000000-0000-4000-8000-000000000003"

func TestRialRemainder(t *testing.T) {
	for total := int64(0); total < 1000; total++ {
		out, e := Split(shared.Money(total), map[string]*big.Rat{a: big.NewRat(1, 1), b: big.NewRat(1, 1), c: big.NewRat(1, 1)})
		if e != nil {
			t.Fatal(e)
		}
		if out[a]+out[b]+out[c] != shared.Money(total) {
			t.Fatal("lost money")
		}
		if out[a] < out[b] || out[b] < out[c] || out[a]-out[c] > 1 {
			t.Fatal("unstable rounding")
		}
	}
}
func TestWeightedExactAndSafeBound(t *testing.T) {
	out, e := Split(shared.Money(shared.MaxMoney), map[string]*big.Rat{a: big.NewRat(1, 10), b: big.NewRat(2, 10)})
	if e != nil || out[a]+out[b] != shared.Money(shared.MaxMoney) {
		t.Fatal(out, e)
	}
}
func TestNumericMoney(t *testing.T) {
	for _, s := range []string{"1200000", "1200000.0", "1.2e6"} {
		var n shared.Money
		if e := json.Unmarshal([]byte(s), &n); e != nil || n != 1200000 {
			t.Fatalf("%s: %v %v", s, n, e)
		}
	}
	for _, s := range []string{`"1200000"`, `12.5`, `-1`, `9007199254740992`, `null`} {
		var n shared.Money
		if json.Unmarshal([]byte(s), &n) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestItemsClaimsAndAdjustments(t *testing.T) {
	n := shared.Money(110)
	now := time.Now()
	e := Expense{ID: shared.ID(), Type: "COST", Payer: a, State: "DRAFT", Split: "ITEM", Amount: &n, PaidOn: &now, Shares: []Share{{UserID: a}, {UserID: b}}, Items: []Item{{ID: shared.ID(), Name: "food", Quantity: "2", Amount: 100, Mode: "QUANTITY", Claims: []Claim{{UserID: a, Quantity: "1"}, {UserID: b, Quantity: "1"}}}}, Adjustments: []Adjustment{{Kind: "TAX", Amount: 20}, {Kind: "DISCOUNT", Amount: 10}}}
	out, err := e.Allocate(true)
	if err != nil || out[a] != 55 || out[b] != 55 {
		t.Fatal(out, err)
	}
	q := shared.Decimal("2")
	if e.Claim(a, e.Items[0].ID, &q, nil) == nil {
		t.Fatal("allowed overclaim")
	}
}
func TestMixedItemModes(t *testing.T) {
	e := Expense{State: "DRAFT", Type: "COST", Split: "ITEM", Shares: []Share{{UserID: a}}, Items: []Item{{ID: shared.ID(), Name: "shared", Quantity: "1", Amount: 20, Mode: "SHARED", Claims: []Claim{}}, {ID: shared.ID(), Name: "quantity", Quantity: "2", Amount: 40, Mode: "QUANTITY", Claims: []Claim{}}}}
	q := shared.Decimal("1")
	if err := e.Claim(a, e.Items[1].ID, &q, nil); err != nil {
		t.Fatal(err)
	}
}
func TestDraftAndFinalRules(t *testing.T) {
	e := Expense{ID: shared.ID(), Type: "COST", State: "DRAFT", Split: "EQUAL"}
	if err := e.Validate(false); err != nil {
		t.Fatal(err)
	}
	e.State = "FINAL"
	if e.Validate(true) == nil {
		t.Fatal("incomplete final accepted")
	}
	if e.Join(a, true, "") == nil {
		t.Fatal("final participation accepted")
	}
}
func TestUnassignedItemsAndDiscount(t *testing.T) {
	n := shared.Money(10)
	e := Expense{Type: "COST", Split: "ITEM", Amount: &n, Shares: []Share{{UserID: a}}, Items: []Item{{ID: shared.ID(), Name: "meal", Quantity: "1", Amount: 10, Mode: "QUANTITY", Claims: []Claim{}}}}
	if _, err := e.Allocate(true); err == nil {
		t.Fatal("unassigned accepted")
	}
	e.Items[0].Claims = []Claim{{UserID: a, Quantity: "1"}}
	e.Adjustments = []Adjustment{{Kind: "DISCOUNT", Amount: 20}}
	if _, err := e.Allocate(true); err == nil {
		t.Fatal("negative share accepted")
	}
}
