package domain

import (
	ex "expensemother/internal/expenses/domain"
	"expensemother/internal/shared"
	"time"
)

type Receipt struct {
	ID          string    `json:"receiptId"`
	GroupID     string    `json:"groupId,omitempty"`
	ExpenseID   string    `json:"expenseId,omitempty"`
	Owner       string    `json:"uploadedByUserId"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
	Path        string    `json:"path"`
}
type Extraction struct {
	ReceiptID    string          `json:"receiptId"`
	Status       string          `json:"status"`
	Merchant     string          `json:"merchant,omitempty"`
	PaidOn       *time.Time      `json:"paidOn,omitempty"`
	Currency     string          `json:"currency"`
	DetectedUnit string          `json:"detectedUnit,omitempty"`
	Amount       *shared.Money   `json:"amount,omitempty"`
	Items        []ex.Item       `json:"items"`
	Adjustments  []ex.Adjustment `json:"adjustments"`
	Warnings     []string        `json:"warnings"`
	RawText      string          `json:"rawText,omitempty"`
}
