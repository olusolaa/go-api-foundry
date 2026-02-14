package ledger

import (
	"context"
	"errors"
	"sort"

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
	GetDerivedBalance(ctx context.Context, accountID string) (int64, error)
	GetAllAccountsForReconciliation(ctx context.Context) ([]AccountReconciliation, error)
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
			return nil, NewAccountNotFoundError()
		}
		return nil, apperrors.NewDatabaseError("failed to fetch account", err)
	}
	return &account, nil
}

func (r *ledgerRepository) ExecuteDoubleEntry(ctx context.Context, cmd DoubleEntryCommand) (*models.Transaction, error) {
	var result *models.Transaction

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Step 1: Check idempotency
		if cmd.IdempotencyKey != "" {
			var existing models.Transaction
			if err := tx.Where("idempotency_key = ?", cmd.IdempotencyKey).
				Preload("Entries").
				First(&existing).Error; err == nil {
				result = &existing
				return nil // Idempotent return
			}
		}

		// Step 2: Deterministic lock ordering — sort account IDs to prevent deadlocks
		accountIDs := []string{cmd.SourceAccountID, cmd.DestAccountID}
		sort.Strings(accountIDs)

		// Step 3: Lock accounts in sorted order (FOR UPDATE on PostgreSQL, no-op on SQLite)
		accounts := make(map[string]*models.Account, 2)
		for _, id := range accountIDs {
			var acc models.Account
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", id).First(&acc).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return NewAccountNotFoundError()
				}
				return apperrors.NewDatabaseError("failed to lock account", err)
			}
			accounts[id] = &acc
		}

		source := accounts[cmd.SourceAccountID]
		dest := accounts[cmd.DestAccountID]

		// Step 4: Validate currencies match
		if source.Currency != dest.Currency {
			return NewCurrencyMismatchError()
		}
		if cmd.Currency != "" && cmd.Currency != source.Currency {
			return NewCurrencyMismatchError()
		}

		// Step 5: Balance check — only USER accounts cannot go negative
		if source.AccountType == models.AccountTypeUser && source.Balance < cmd.Amount {
			return NewInsufficientFundsError()
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
			if isDuplicateKey(err) {
				// Concurrent idempotent request — reload and return
				var existing models.Transaction
				if loadErr := tx.Where("idempotency_key = ?", cmd.IdempotencyKey).
					Preload("Entries").
					First(&existing).Error; loadErr == nil {
					result = &existing
					return nil
				}
			}
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
		if err := tx.Model(source).Updates(map[string]interface{}{
			"balance": sourceBalanceAfter,
			"version": source.Version + 1,
		}).Error; err != nil {
			return apperrors.NewDatabaseError("failed to update source account", err)
		}

		// Step 10: Update dest account balance and version
		if err := tx.Model(dest).Updates(map[string]interface{}{
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

func (r *ledgerRepository) GetDerivedBalance(ctx context.Context, accountID string) (int64, error) {
	var creditSum, debitSum int64

	// Sum all credits
	if err := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("account_id = ? AND entry_type = ?", accountID, models.EntryTypeCredit).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&creditSum).Error; err != nil {
		return 0, apperrors.NewDatabaseError("failed to calculate credit sum", err)
	}

	// Sum all debits
	if err := r.db.WithContext(ctx).
		Model(&models.LedgerEntry{}).
		Where("account_id = ? AND entry_type = ?", accountID, models.EntryTypeDebit).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&debitSum).Error; err != nil {
		return 0, apperrors.NewDatabaseError("failed to calculate debit sum", err)
	}

	return creditSum - debitSum, nil
}

func (r *ledgerRepository) GetAllAccountsForReconciliation(ctx context.Context) ([]AccountReconciliation, error) {
	var accounts []models.Account
	if err := r.db.WithContext(ctx).Find(&accounts).Error; err != nil {
		return nil, apperrors.NewDatabaseError("failed to fetch accounts for reconciliation", err)
	}

	results := make([]AccountReconciliation, 0, len(accounts))
	for _, acc := range accounts {
		derived, err := r.GetDerivedBalance(ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		results = append(results, AccountReconciliation{
			AccountID:      acc.ID,
			AccountName:    acc.Name,
			AccountType:    acc.AccountType,
			CachedBalance:  acc.Balance,
			DerivedBalance: derived,
			IsConsistent:   acc.Balance == derived,
		})
	}

	return results, nil
}

func isDuplicateKey(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || apperrors.IsDuplicateKeyError(err)
}
