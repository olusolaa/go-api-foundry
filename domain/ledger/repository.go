package ledger

import (
	"context"
	"errors"
	"slices"

	"github.com/akeren/go-api-foundry/internal/models"
	apperrors "github.com/akeren/go-api-foundry/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LedgerRepository interface {
	CreateAccount(ctx context.Context, account *models.Account) (*models.Account, error)
	GetAccountByID(ctx context.Context, id string) (*models.Account, error)
	ExecuteDoubleEntry(ctx context.Context, cmd DoubleEntryCommand) (*models.Transaction, error)
	GetTransactionsByAccountID(ctx context.Context, accountID string, limit, offset int) ([]models.Transaction, error)
	GetBalanceSnapshot(ctx context.Context, accountID string) (*BalanceSnapshot, error)
	GetAllAccountsForReconciliation(ctx context.Context) ([]AccountReconciliation, error)
	GetLedgerTotals(ctx context.Context) (totalDebits, totalCredits int64, err error)
}

// DoubleEntryCommand encapsulates all data needed for a double-entry transaction.
type DoubleEntryCommand struct {
	SourceAccountID string
	DestAccountID   string
	Amount          int64
	Currency        string
	TransactionType string
	IdempotencyKey  string
	Description     string
}

// AccountReconciliation holds both cached and derived balances for an account.
type AccountReconciliation struct {
	AccountID      string `json:"account_id"`
	AccountName    string `json:"account_name"`
	AccountType    string `json:"account_type"`
	CachedBalance  int64  `json:"cached_balance"`
	DerivedBalance int64  `json:"derived_balance"`
	IsConsistent   bool   `json:"is_consistent"`
}

// BalanceSnapshot holds cached and derived balances read within a single transaction.
type BalanceSnapshot struct {
	AccountID      string
	CachedBalance  int64
	DerivedBalance int64
	Currency       string
}

type ledgerRepository struct {
	db *gorm.DB
}

func NewLedgerRepository(db *gorm.DB) LedgerRepository {
	return &ledgerRepository{db: db}
}

func (r *ledgerRepository) CreateAccount(ctx context.Context, account *models.Account) (*models.Account, error) {
	if err := r.db.WithContext(ctx).Create(account).Error; err != nil {
		if isDuplicateKey(err) {
			return nil, apperrors.NewConflictError("account already exists", err)
		}
		return nil, apperrors.NewDatabaseError("unable to create account", err)
	}
	return account, nil
}

func (r *ledgerRepository) GetAccountByID(ctx context.Context, id string) (*models.Account, error) {
	var account models.Account
	if err := r.db.WithContext(ctx).First(&account, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, apperrors.NewDatabaseError("failed to fetch account", err)
	}
	return &account, nil
}

func (r *ledgerRepository) ExecuteDoubleEntry(ctx context.Context, cmd DoubleEntryCommand) (*models.Transaction, error) {
	var result *models.Transaction

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Step 1: Deterministic lock ordering — sort account IDs to prevent deadlocks
		accountIDs := []string{cmd.SourceAccountID, cmd.DestAccountID}
		slices.Sort(accountIDs)

		// Step 2: Lock accounts in sorted order (FOR UPDATE on PostgreSQL, no-op on SQLite)
		accounts := make(map[string]*models.Account, 2)
		for _, id := range accountIDs {
			var acc models.Account
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", id).First(&acc).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAccountNotFound
				}
				return apperrors.NewDatabaseError("failed to lock account", err)
			}
			accounts[id] = &acc
		}

		// Step 3: Check idempotency AFTER acquiring locks. Because all operations
		// involving the same accounts serialize through FOR UPDATE, by this point
		// any previously concurrent transaction has already committed. This avoids
		// the PostgreSQL "current transaction is aborted" problem that occurs when
		// a UNIQUE constraint violation is handled with a fallback SELECT.
		if cmd.IdempotencyKey != "" {
			var existing models.Transaction
			if err := tx.Where("idempotency_key = ?", cmd.IdempotencyKey).
				Preload("Entries").
				First(&existing).Error; err == nil {
				if existing.Amount != cmd.Amount || existing.TransactionType != cmd.TransactionType {
					return ErrIdempotencyConflict
				}
				result = &existing
				return nil // Idempotent return
			}
		}

		source := accounts[cmd.SourceAccountID]
		dest := accounts[cmd.DestAccountID]

		// Step 4: Validate currencies match
		if source.Currency != dest.Currency {
			return ErrCurrencyMismatch
		}
		if cmd.Currency != "" && cmd.Currency != source.Currency {
			return ErrCurrencyMismatch
		}

		// Step 5: Balance check — only USER accounts cannot go negative
		if source.AccountType == models.AccountTypeUser && source.Balance < cmd.Amount {
			return ErrInsufficientFunds
		}

		// Step 6: Create transaction record
		txn := models.Transaction{
			IdempotencyKey:  cmd.IdempotencyKey,
			TransactionType: cmd.TransactionType,
			Amount:          cmd.Amount,
			Currency:        source.Currency,
			Description:     cmd.Description,
		}
		if err := tx.Create(&txn).Error; err != nil {
			return apperrors.NewDatabaseError("failed to create transaction", err)
		}

		// Step 7: Create DEBIT entry (source account)
		sourceBalanceAfter := source.Balance - cmd.Amount
		debitEntry := models.LedgerEntry{
			TransactionID: txn.ID,
			AccountID:     source.ID,
			EntryType:     models.EntryTypeDebit,
			Amount:        cmd.Amount,
			BalanceAfter:  sourceBalanceAfter,
		}
		if err := tx.Create(&debitEntry).Error; err != nil {
			return apperrors.NewDatabaseError("failed to create debit entry", err)
		}

		// Step 8: Create CREDIT entry (dest account)
		destBalanceAfter := dest.Balance + cmd.Amount
		creditEntry := models.LedgerEntry{
			TransactionID: txn.ID,
			AccountID:     dest.ID,
			EntryType:     models.EntryTypeCredit,
			Amount:        cmd.Amount,
			BalanceAfter:  destBalanceAfter,
		}
		if err := tx.Create(&creditEntry).Error; err != nil {
			return apperrors.NewDatabaseError("failed to create credit entry", err)
		}

		// Step 9: Update source account balance and version
		if err := tx.Model(source).Updates(map[string]any{
			"balance": sourceBalanceAfter,
			"version": source.Version + 1,
		}).Error; err != nil {
			return apperrors.NewDatabaseError("failed to update source account", err)
		}

		// Step 10: Update dest account balance and version
		if err := tx.Model(dest).Updates(map[string]any{
			"balance": destBalanceAfter,
			"version": dest.Version + 1,
		}).Error; err != nil {
			return apperrors.NewDatabaseError("failed to update destination account", err)
		}

		txn.Entries = []models.LedgerEntry{debitEntry, creditEntry}
		result = &txn
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *ledgerRepository) GetTransactionsByAccountID(ctx context.Context, accountID string, limit, offset int) ([]models.Transaction, error) {
	// Subquery: find transaction IDs that involve this account
	subQuery := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select("DISTINCT transaction_id").
		Where("account_id = ?", accountID)

	query := r.db.WithContext(ctx).
		Where("id IN (?)", subQuery).
		Preload("Entries").
		Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	var transactions []models.Transaction
	if err := query.Find(&transactions).Error; err != nil {
		return nil, apperrors.NewDatabaseError("failed to fetch transactions", err)
	}

	return transactions, nil
}

func (r *ledgerRepository) GetBalanceSnapshot(ctx context.Context, accountID string) (*BalanceSnapshot, error) {
	var snapshot BalanceSnapshot

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account models.Account
		if err := tx.First(&account, "id = ?", accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAccountNotFound
			}
			return apperrors.NewDatabaseError("failed to fetch account", err)
		}

		var derived int64
		if err := tx.Model(&models.LedgerEntry{}).
			Where("account_id = ?", accountID).
			Select("COALESCE(SUM(CASE WHEN entry_type = ? THEN amount ELSE -amount END), 0)", models.EntryTypeCredit).
			Scan(&derived).Error; err != nil {
			return apperrors.NewDatabaseError("failed to calculate derived balance", err)
		}

		snapshot = BalanceSnapshot{
			AccountID:      account.ID,
			CachedBalance:  account.Balance,
			DerivedBalance: derived,
			Currency:       account.Currency,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *ledgerRepository) GetAllAccountsForReconciliation(ctx context.Context) ([]AccountReconciliation, error) {
	var results []AccountReconciliation

	err := r.db.WithContext(ctx).
		Table("accounts a").
		Select(`a.id AS account_id,
			a.name AS account_name,
			a.account_type,
			a.balance AS cached_balance,
			COALESCE(SUM(CASE WHEN le.entry_type = ? THEN le.amount ELSE 0 END), 0) -
			COALESCE(SUM(CASE WHEN le.entry_type = ? THEN le.amount ELSE 0 END), 0) AS derived_balance`,
			models.EntryTypeCredit, models.EntryTypeDebit).
		Joins("LEFT JOIN ledger_entries le ON le.account_id = a.id").
		Group("a.id, a.name, a.account_type, a.balance").
		Scan(&results).Error
	if err != nil {
		return nil, apperrors.NewDatabaseError("failed to run reconciliation", err)
	}

	for i := range results {
		results[i].IsConsistent = results[i].CachedBalance == results[i].DerivedBalance
	}

	return results, nil
}

func (r *ledgerRepository) GetLedgerTotals(ctx context.Context) (totalDebits, totalCredits int64, err error) {
	type totals struct {
		TotalDebits  int64
		TotalCredits int64
	}
	var t totals

	if err := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Select(`COALESCE(SUM(CASE WHEN entry_type = ? THEN amount ELSE 0 END), 0) AS total_debits,
			COALESCE(SUM(CASE WHEN entry_type = ? THEN amount ELSE 0 END), 0) AS total_credits`,
			models.EntryTypeDebit, models.EntryTypeCredit).
		Scan(&t).Error; err != nil {
		return 0, 0, apperrors.NewDatabaseError("failed to calculate ledger totals", err)
	}

	return t.TotalDebits, t.TotalCredits, nil
}

func isDuplicateKey(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || apperrors.IsDuplicateKeyError(err)
}
