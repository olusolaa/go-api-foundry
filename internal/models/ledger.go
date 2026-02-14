package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Account types
const (
	AccountTypeUser   = "USER"
	AccountTypeSystem = "SYSTEM"
)

// Transaction types
const (
	TransactionTypeDeposit    = "DEPOSIT"
	TransactionTypeWithdrawal = "WITHDRAWAL"
	TransactionTypeTransfer   = "TRANSFER"
)

// Entry types
const (
	EntryTypeDebit  = "DEBIT"
	EntryTypeCredit = "CREDIT"
)

// SystemAccountID is the well-known UUID for the external funding source.
const SystemAccountID = "00000000-0000-0000-0000-000000000001"

type Account struct {
	ID          string    `gorm:"type:text;primaryKey" json:"id"`
	Name        string    `gorm:"not null" json:"name"`
	AccountType string    `gorm:"not null" json:"account_type"`
	Currency    string    `gorm:"type:char(3);not null;default:USD" json:"currency"`
	Balance     int64     `gorm:"not null;default:0" json:"balance"`
	Version     int64     `gorm:"not null;default:0" json:"version"`
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (a *Account) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

type Transaction struct {
	ID              string    `gorm:"type:text;primaryKey" json:"id"`
	IdempotencyKey  string    `gorm:"uniqueIndex" json:"idempotency_key"`
	TransactionType string    `gorm:"not null" json:"transaction_type"`
	Amount          int64     `gorm:"not null" json:"amount"`
	Currency        string    `gorm:"type:char(3);not null;default:USD" json:"currency"`
	Description     string    `json:"description"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`

	Entries []LedgerEntry `gorm:"foreignKey:TransactionID" json:"entries,omitempty"`
}

func (t *Transaction) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return nil
}

type LedgerEntry struct {
	ID            string    `gorm:"type:text;primaryKey" json:"id"`
	TransactionID string    `gorm:"not null;index" json:"transaction_id"`
	AccountID     string    `gorm:"not null" json:"account_id"`
	EntryType     string    `gorm:"not null" json:"entry_type"`
	Amount        int64     `gorm:"not null" json:"amount"`
	BalanceAfter  int64     `gorm:"not null" json:"balance_after"`
	CreatedAt     time.Time `gorm:"not null" json:"created_at"`

	Transaction Transaction `gorm:"foreignKey:TransactionID" json:"-"`
	Account     Account     `gorm:"foreignKey:AccountID" json:"-"`
}

func (e *LedgerEntry) BeforeCreate(tx *gorm.DB) error {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	return nil
}
