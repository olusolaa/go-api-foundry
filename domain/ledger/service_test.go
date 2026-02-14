package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/akeren/go-api-foundry/internal/log"
	"github.com/akeren/go-api-foundry/internal/models"
	apperrors "github.com/akeren/go-api-foundry/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func newTestService(t *testing.T) (*MockLedgerRepository, LedgerService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockRepo := NewMockLedgerRepository(ctrl)
	logger := log.NewLoggerWithJSONOutput()
	service := NewLedgerService(logger, mockRepo)
	return mockRepo, service
}

func TestCreateAccount(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &CreateAccountRequest{Name: "Alice", Currency: "USD"}
		expected := &models.Account{
			ID:          "acc-1",
			Name:        "Alice",
			AccountType: models.AccountTypeUser,
			Currency:    "USD",
			CreatedAt:   time.Now(),
		}

		mockRepo.EXPECT().CreateAccount(gomock.Any(), gomock.Any()).Return(expected, nil)

		result, err := service.CreateAccount(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "Alice", result.Name)
		assert.Equal(t, "USD", result.Currency)
		assert.Equal(t, "USER", result.AccountType)
	})

	t.Run("nil request", func(t *testing.T) {
		_, service := newTestService(t)

		result, err := service.CreateAccount(context.Background(), nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, apperrors.ErrorTypeInvalidRequest, apperrors.GetErrorType(err))
	})

	t.Run("default currency", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &CreateAccountRequest{Name: "Bob"}
		mockRepo.EXPECT().CreateAccount(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, acc *models.Account) (*models.Account, error) {
				assert.Equal(t, "USD", acc.Currency)
				acc.ID = "acc-2"
				acc.CreatedAt = time.Now()
				return acc, nil
			},
		)

		result, err := service.CreateAccount(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, "USD", result.Currency)
	})

	t.Run("repository error", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &CreateAccountRequest{Name: "Charlie"}
		mockRepo.EXPECT().CreateAccount(gomock.Any(), gomock.Any()).Return(nil, apperrors.NewDatabaseError("db error", nil))

		result, err := service.CreateAccount(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestGetAccount(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		expected := &models.Account{
			ID:          "acc-1",
			Name:        "Alice",
			AccountType: models.AccountTypeUser,
			Currency:    "USD",
			Balance:     10000,
			CreatedAt:   time.Now(),
		}
		mockRepo.EXPECT().GetAccountByID(gomock.Any(), "acc-1").Return(expected, nil)

		result, err := service.GetAccount(context.Background(), "acc-1")
		assert.NoError(t, err)
		assert.Equal(t, "acc-1", result.ID)
		assert.Equal(t, int64(10000), result.Balance)
	})

	t.Run("empty ID", func(t *testing.T) {
		_, service := newTestService(t)

		result, err := service.GetAccount(context.Background(), "")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("not found", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		mockRepo.EXPECT().GetAccountByID(gomock.Any(), "nonexistent").Return(nil, ErrAccountNotFound)

		result, err := service.GetAccount(context.Background(), "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrAccountNotFound)
	})
}

func TestDeposit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &DepositRequest{
			Amount:         5000,
			IdempotencyKey: "dep-1",
			Description:    "test deposit",
		}

		expectedTxn := &models.Transaction{
			ID:              "txn-1",
			IdempotencyKey:  "dep-1",
			TransactionType: models.TransactionTypeDeposit,
			Amount:          5000,
			Currency:        "USD",
			CreatedAt:       time.Now(),
			Entries: []models.LedgerEntry{
				{ID: "e1", EntryType: models.EntryTypeDebit, Amount: 5000, CreatedAt: time.Now()},
				{ID: "e2", EntryType: models.EntryTypeCredit, Amount: 5000, CreatedAt: time.Now()},
			},
		}

		mockRepo.EXPECT().ExecuteDoubleEntry(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd DoubleEntryCommand) (*models.Transaction, error) {
				assert.Equal(t, models.SystemAccountID, cmd.SourceAccountID)
				assert.Equal(t, "acc-1", cmd.DestAccountID)
				assert.Equal(t, int64(5000), cmd.Amount)
				assert.Equal(t, models.TransactionTypeDeposit, cmd.TransactionType)
				return expectedTxn, nil
			},
		)

		result, err := service.Deposit(context.Background(), "acc-1", req)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "txn-1", result.ID)
		assert.Len(t, result.Entries, 2)
	})

	validationTests := []struct {
		name      string
		accountID string
		req       *DepositRequest
		wantErr   error
	}{
		{"nil request", "acc-1", nil, nil},
		{"empty account ID", "", &DepositRequest{Amount: 5000, IdempotencyKey: "k"}, nil},
		{"zero amount", "acc-1", &DepositRequest{Amount: 0, IdempotencyKey: "k"}, ErrInvalidAmount},
		{"system account", models.SystemAccountID, &DepositRequest{Amount: 5000, IdempotencyKey: "k"}, ErrSystemAccountForbidden},
	}

	for _, tt := range validationTests {
		t.Run(tt.name, func(t *testing.T) {
			_, service := newTestService(t)
			result, err := service.Deposit(context.Background(), tt.accountID, tt.req)
			assert.Error(t, err)
			assert.Nil(t, result)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestWithdraw(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &WithdrawRequest{
			Amount:         3000,
			IdempotencyKey: "wd-1",
		}

		expectedTxn := &models.Transaction{
			ID:              "txn-2",
			TransactionType: models.TransactionTypeWithdrawal,
			Amount:          3000,
			Currency:        "USD",
			CreatedAt:       time.Now(),
		}

		mockRepo.EXPECT().ExecuteDoubleEntry(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd DoubleEntryCommand) (*models.Transaction, error) {
				assert.Equal(t, "acc-1", cmd.SourceAccountID)
				assert.Equal(t, models.SystemAccountID, cmd.DestAccountID)
				assert.Equal(t, models.TransactionTypeWithdrawal, cmd.TransactionType)
				return expectedTxn, nil
			},
		)

		result, err := service.Withdraw(context.Background(), "acc-1", req)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("insufficient funds", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &WithdrawRequest{
			Amount:         99999,
			IdempotencyKey: "wd-2",
		}

		mockRepo.EXPECT().ExecuteDoubleEntry(gomock.Any(), gomock.Any()).Return(nil, ErrInsufficientFunds)

		result, err := service.Withdraw(context.Background(), "acc-1", req)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrInsufficientFunds)
	})

	t.Run("system account rejected", func(t *testing.T) {
		_, service := newTestService(t)

		req := &WithdrawRequest{Amount: 5000, IdempotencyKey: "wd-sys"}
		result, err := service.Withdraw(context.Background(), models.SystemAccountID, req)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrSystemAccountForbidden)
	})
}

func TestTransfer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		req := &TransferRequest{
			SourceAccountID: "acc-1",
			DestAccountID:   "acc-2",
			Amount:          2000,
			IdempotencyKey:  "xfr-1",
		}

		expectedTxn := &models.Transaction{
			ID:              "txn-3",
			TransactionType: models.TransactionTypeTransfer,
			Amount:          2000,
			Currency:        "USD",
			CreatedAt:       time.Now(),
		}

		mockRepo.EXPECT().ExecuteDoubleEntry(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd DoubleEntryCommand) (*models.Transaction, error) {
				assert.Equal(t, "acc-1", cmd.SourceAccountID)
				assert.Equal(t, "acc-2", cmd.DestAccountID)
				assert.Equal(t, models.TransactionTypeTransfer, cmd.TransactionType)
				return expectedTxn, nil
			},
		)

		result, err := service.Transfer(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	validationTests := []struct {
		name    string
		req     *TransferRequest
		wantErr error
	}{
		{"nil request", nil, nil},
		{
			"empty source account",
			&TransferRequest{SourceAccountID: "", DestAccountID: "acc-2", Amount: 1000, IdempotencyKey: "k"},
			nil,
		},
		{
			"self transfer",
			&TransferRequest{SourceAccountID: "acc-1", DestAccountID: "acc-1", Amount: 1000, IdempotencyKey: "k"},
			ErrSelfTransfer,
		},
		{
			"negative amount",
			&TransferRequest{SourceAccountID: "acc-1", DestAccountID: "acc-2", Amount: -100, IdempotencyKey: "k"},
			ErrInvalidAmount,
		},
		{
			"system account as source",
			&TransferRequest{SourceAccountID: models.SystemAccountID, DestAccountID: "acc-2", Amount: 1000, IdempotencyKey: "k"},
			ErrSystemAccountForbidden,
		},
		{
			"system account as dest",
			&TransferRequest{SourceAccountID: "acc-1", DestAccountID: models.SystemAccountID, Amount: 1000, IdempotencyKey: "k"},
			ErrSystemAccountForbidden,
		},
	}

	for _, tt := range validationTests {
		t.Run(tt.name, func(t *testing.T) {
			_, service := newTestService(t)
			result, err := service.Transfer(context.Background(), tt.req)
			assert.Error(t, err)
			assert.Nil(t, result)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestGetBalance(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		snapshot := &BalanceSnapshot{
			AccountID:      "acc-1",
			CachedBalance:  10000,
			DerivedBalance: 10000,
			Currency:       "USD",
		}
		mockRepo.EXPECT().GetBalanceSnapshot(gomock.Any(), "acc-1").Return(snapshot, nil)

		result, err := service.GetBalance(context.Background(), "acc-1")
		assert.NoError(t, err)
		assert.Equal(t, int64(10000), result.CachedBalance)
		assert.Equal(t, int64(10000), result.DerivedBalance)
		assert.True(t, result.IsConsistent)
	})

	t.Run("inconsistent", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		snapshot := &BalanceSnapshot{
			AccountID:      "acc-1",
			CachedBalance:  10000,
			DerivedBalance: 9500,
			Currency:       "USD",
		}
		mockRepo.EXPECT().GetBalanceSnapshot(gomock.Any(), "acc-1").Return(snapshot, nil)

		result, err := service.GetBalance(context.Background(), "acc-1")
		assert.NoError(t, err)
		assert.Equal(t, int64(10000), result.CachedBalance)
		assert.Equal(t, int64(9500), result.DerivedBalance)
		assert.False(t, result.IsConsistent)
	})

	t.Run("empty ID", func(t *testing.T) {
		_, service := newTestService(t)

		result, err := service.GetBalance(context.Background(), "")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestGetTransactions(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		account := &models.Account{ID: "acc-1"}
		txns := []models.Transaction{
			{
				ID:              "txn-1",
				TransactionType: models.TransactionTypeDeposit,
				Amount:          5000,
				Currency:        "USD",
				CreatedAt:       time.Now(),
			},
		}

		mockRepo.EXPECT().GetAccountByID(gomock.Any(), "acc-1").Return(account, nil)
		mockRepo.EXPECT().GetTransactionsByAccountID(gomock.Any(), "acc-1", 50, 0).Return(txns, nil)

		result, err := service.GetTransactions(context.Background(), "acc-1", 50, 0)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("empty ID", func(t *testing.T) {
		_, service := newTestService(t)

		result, err := service.GetTransactions(context.Background(), "", 50, 0)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("account not found", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		mockRepo.EXPECT().GetAccountByID(gomock.Any(), "nonexistent").Return(nil, ErrAccountNotFound)

		result, err := service.GetTransactions(context.Background(), "nonexistent", 50, 0)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrAccountNotFound)
	})
}

func TestReconcile(t *testing.T) {
	t.Run("all consistent", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		results := []AccountReconciliation{
			{AccountID: "acc-1", CachedBalance: 10000, DerivedBalance: 10000, IsConsistent: true},
			{AccountID: "acc-2", CachedBalance: 5000, DerivedBalance: 5000, IsConsistent: true},
		}

		mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(results, nil)
		mockRepo.EXPECT().GetLedgerTotals(gomock.Any()).Return(int64(15000), int64(15000), nil)

		resp, err := service.Reconcile(context.Background())
		assert.NoError(t, err)
		assert.True(t, resp.AllConsistent)
		assert.True(t, resp.LedgerBalanced)
		assert.Equal(t, int64(15000), resp.TotalDebits)
		assert.Equal(t, int64(15000), resp.TotalCredits)
		assert.Len(t, resp.Accounts, 2)
	})

	t.Run("with inconsistency", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		results := []AccountReconciliation{
			{AccountID: "acc-1", CachedBalance: 10000, DerivedBalance: 10000, IsConsistent: true},
			{AccountID: "acc-2", CachedBalance: 5000, DerivedBalance: 4500, IsConsistent: false},
		}

		mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(results, nil)
		mockRepo.EXPECT().GetLedgerTotals(gomock.Any()).Return(int64(15000), int64(15000), nil)

		resp, err := service.Reconcile(context.Background())
		assert.NoError(t, err)
		assert.False(t, resp.AllConsistent)
	})

	t.Run("ledger unbalanced", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		results := []AccountReconciliation{
			{AccountID: "acc-1", CachedBalance: 10000, DerivedBalance: 10000, IsConsistent: true},
		}

		mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(results, nil)
		mockRepo.EXPECT().GetLedgerTotals(gomock.Any()).Return(int64(10000), int64(9000), nil)

		resp, err := service.Reconcile(context.Background())
		assert.NoError(t, err)
		assert.False(t, resp.AllConsistent)
		assert.False(t, resp.LedgerBalanced)
	})

	t.Run("repository error", func(t *testing.T) {
		mockRepo, service := newTestService(t)

		mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(nil, apperrors.NewDatabaseError("db error", nil))

		resp, err := service.Reconcile(context.Background())
		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}
