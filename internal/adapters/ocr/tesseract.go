package ocr

import (
	"bytes"
	"context"
	ex "expensemother/internal/expenses/domain"
	re "expensemother/internal/receipts/domain"
	"expensemother/internal/shared"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func Normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '۰' && r <= '۹' {
			return '0' + r - '۰'
		}
		if r >= '٠' && r <= '٩' {
			return '0' + r - '٠'
		}
		if r == '٬' {
			return ','
		}
		if r == '٫' {
			return '.'
		}
		return r
	}, s)
}

type Tesseract struct{ Binary string }

func (t Tesseract) Extract(ctx context.Context, path, id string, languages []string) re.Extraction {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	lang := []string{}
	for _, v := range languages {
		if v == "fa" {
			lang = append(lang, "fas")
		} else if v == "en" {
			lang = append(lang, "eng")
		}
	}
	if len(lang) == 0 {
		lang = []string{"fas", "eng"}
	}
	bin := t.Binary
	if bin == "" {
		bin = "tesseract"
	}
	cmd := exec.CommandContext(ctx, bin, path, "stdout", "-l", strings.Join(lang, "+"), "--psm", "6")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if cmd.Run() != nil {
		return re.Extraction{ReceiptID: id, Status: "FAILED", Currency: "Rials", Items: []ex.Item{}, Adjustments: []ex.Adjustment{}, Warnings: []string{"OCR unavailable or timed out. The image is saved; enter the receipt manually."}}
	}
	return Parse(out.String(), id)
}

var line = regexp.MustCompile(`^(.+?)\s+([0-9][0-9,]*)\s*$`)
var receiptDate = regexp.MustCompile(`\b([12][0-9]{3})[-/]([01][0-9])[-/]([0-3][0-9])\b`)

func Parse(raw, id string) re.Extraction {
	r := re.Extraction{ReceiptID: id, Status: "PARTIAL", Currency: "Rials", RawText: raw, Items: []ex.Item{}, Adjustments: []ex.Adjustment{}, Warnings: []string{"Review all items, quantities and prices. OCR suggestions do not finalize an expense."}}
	normalized := Normalize(raw)
	if date := receiptDate.FindStringSubmatch(normalized); len(date) > 0 {
		year, _ := strconv.Atoi(date[1])
		if year >= 1900 {
			d, err := time.Parse("2006-01-02", strings.Join(date[1:], "-"))
			if err == nil {
				r.PaidOn = &d
				r.Warnings = append(r.Warnings, "Receipt date inferred without a time; verify before saving.")
			}
		} else {
			r.Warnings = append(r.Warnings, "Persian-calendar date detected. Confirm the payment date manually.")
		}
	}
	toman := strings.Contains(strings.ToLower(normalized), "toman") || strings.Contains(normalized, "تومان")
	if toman {
		r.DetectedUnit = "Toman"
		r.Warnings = append(r.Warnings, "Receipt appears to use Toman. Confirm and enter Rial values before saving; no automatic conversion was made.")
	}
	for _, s := range strings.Split(normalized, "\n") {
		if r.Merchant == "" && strings.TrimSpace(s) != "" && len(strings.Fields(s)) < 12 && line.FindStringSubmatch(strings.TrimSpace(s)) == nil && receiptDate.FindString(s) == "" {
			r.Merchant = strings.TrimSpace(s)
		}
		m := line.FindStringSubmatch(strings.TrimSpace(s))
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		number := m[2]
		if strings.Contains(number, ",") {
			parts := strings.Split(number, ",")
			valid := len(parts[0]) <= 3
			for _, part := range parts[1:] {
				valid = valid && len(part) == 3
			}
			if !valid {
				r.Warnings = append(r.Warnings, "Ambiguous number: "+number)
				continue
			}
		}
		n, e := strconv.ParseInt(strings.ReplaceAll(number, ",", ""), 10, 64)
		if e != nil || n > shared.MaxMoney {
			continue
		}
		amount := shared.Money(n)
		lower := strings.ToLower(name)
		if strings.Contains(lower, "total") || strings.Contains(name, "جمع") || strings.Contains(name, "قابل پرداخت") {
			if !toman {
				r.Amount = &amount
			}
			continue
		}
		if strings.Contains(lower, "tax") || strings.Contains(name, "مالیات") {
			r.Adjustments = append(r.Adjustments, ex.Adjustment{Kind: "TAX", Name: name, Amount: amount})
			continue
		}
		if strings.Contains(lower, "discount") || strings.Contains(name, "تخفیف") {
			r.Adjustments = append(r.Adjustments, ex.Adjustment{Kind: "DISCOUNT", Name: name, Amount: amount})
			continue
		}
		r.Items = append(r.Items, ex.Item{ID: shared.ID(), Name: name, Quantity: "1", Amount: amount, Mode: "QUANTITY", Claims: []ex.Claim{}})
	}
	if len(r.Items) == 0 {
		r.Warnings = append(r.Warnings, "No reliable item lines found; add items manually.")
	}
	return r
}
