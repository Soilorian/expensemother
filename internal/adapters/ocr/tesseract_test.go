package ocr

import "testing"

func TestPersianDigitsAndTomanWarning(t *testing.T) {
	v := Parse("غذا ۱۲۰٬۰۰۰\nجمع ۱۲۰٬۰۰۰ تومان", "r")
	if len(v.Items) != 1 || v.Items[0].Amount != 120000 || v.DetectedUnit != "Toman" || v.Amount != nil {
		t.Fatal(v)
	}
}
func TestEnglishAndAmbiguousNumbers(t *testing.T) {
	v := Parse("Dinner 120,000\nTax 12,000\nTotal 132,000\nBad 12,34", "r")
	if len(v.Items) != 1 || v.Amount == nil || *v.Amount != 132000 || len(v.Adjustments) != 1 {
		t.Fatal(v)
	}
}
