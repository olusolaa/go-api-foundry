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
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockRepo := NewMockLedgerRepository(ctrl)
	logger := log.NewLoggerWithJSONOutput()
	service := NewLedgerService(logger, mockRepo)
	return mockRepo, service
}

func TestCreateAccount_Success(t *testing.T) {
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
}

func TestCreateAccount_NilRequest(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.CreateAccount(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, apperrors.ErrorTypeInvalidRequest, apperrors.GetErrorType(err))
}

func TestCreateAccount_DefaultCurrency(t *testing.T) {
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
}

func TestCreateAccount_RepositoryError(t *testing.T) {
	mockRepo, service := newTestService(t)

	req := &CreateAccountRequest{Name: "Charlie"}
	mockRepo.EXPECT().CreateAccount(gomock.Any(), gomock.Any()).Return(nil, apperrors.NewDatabaseError("db error", nil))

	result, err := service.CreateAccount(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetAccount_Success(t *testing.T) {
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
}

func TestGetAccount_EmptyID(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.GetAccount(context.Background(), "")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetAccount_NotFound(t *testing.T) {
	mockRepo, service := newTestService(t)

	mockRepo.EXPECT().GetAccountByID(gomock.Any(), "nonexistent").Return(nil, NewAccountNotFoundError())

	result, err := service.GetAccount(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, apperrors.ErrorTypeNotFound, apperrors.GetErrorType(err))
}

func TestDeposit_Success(t *testing.T) {
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
}

func TestDeposit_NilRequest(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.Deposit(context.Background(), "acc-1", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestDeposit_EmptyAccountID(t *testing.T) {
	_, service := newTestService(t)

	req := &DepositRequest{Amount: 5000, IdempotencyKey: "dep-1"}
	result, err := service.Deposit(context.Background(), "", req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestDeposit_InvalidAmount(t *testing.T) {
	_, service := newTestService(t)

	req := &DepositRequest{Amount: 0, IdempotencyKey: "dep-1"}
	result, err := service.Deposit(context.Background(), "acc-1", req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestWithdraw_Success(t *testing.T) {
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
}

func TestWithdraw_InsufficientFunds(t *testing.T) {
	mockRepo, service := newTestService(t)

	req := &WithdrawRequest{
		Amount:         99999,
		IdempotencyKey: "wd-2",
	}

	mockRepo.EXPECT().ExecuteDoubleEntry(gomock.Any(), gomock.Any()).Return(nil, NewInsufficientFundsError())

	result, err := service.Withdraw(context.Background(), "acc-1", req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, apperrors.ErrorTypeInvalidRequest, apperrors.GetErrorType(err))
}

func TestTransfer_Success(t *testing.T) {
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
}

func TestTransfer_SelfTransfer(t *testing.T) {
	_, service := newTestService(t)

	req := &TransferRequest{
		SourceAccountID: "acc-1",
		DestAccountID:   "acc-1",
		Amount:          1000,
		IdempotencyKey:  "xfr-2",
	}

	result, err := service.Transfer(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestTransfer_NilRequest(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.Transfer(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestTransfer_EmptyAccountIDs(t *testing.T) {
	_, service := newTestService(t)

	req := &TransferRequest{
		SourceAccountID: "",
		DestAccountID:   "acc-2",
		Amount:          1000,
		IdempotencyKey:  "xfr-3",
	}

	result, err := service.Transfer(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestTransfer_InvalidAmount(t *testing.T) {
	_, service := newTestService(t)

	req := &TransferRequest{
		SourceAccountID: "acc-1",
		DestAccountID:   "acc-2",
		Amount:          -100,
		IdempotencyKey:  "xfr-4",
	}

	result, err := service.Transfer(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetBalance_Success(t *testing.T) {
	mockRepo, service := newTestService(t)

	account := &models.Account{
		ID:       "acc-1",
		Balance:  10000,
		Currency: "USD",
	}

	mockRepo.EXPECT().GetAccountByID(gomock.Any(), "acc-1").Return(account, nil)
	mockRepo.EXPECT().GetDerivedBalance(gomock.Any(), "acc-1").Return(int64(10000), nil)

	result, err := service.GetBalance(context.Background(), "acc-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(10000), result.CachedBalance)
	assert.Equal(t, int64(10000), result.DerivedBalance)
	assert.True(t, result.IsConsistent)
}

func TestGetBalance_Inconsistent(t *testing.T) {
	mockRepo, service := newTestService(t)

	account := &models.Account{
		ID:       "acc-1",
		Balance:  10000,
		Currency: "USD",
	}

	mockRepo.EXPECT().GetAccountByID(gomock.Any(), "acc-1").Return(account, nil)
	mockRepo.EXPECT().GetDerivedBalance(gomock.Any(), "acc-1").Return(int64(9500), nil)

	result, err := service.GetBalance(context.Background(), "acc-1")
	assert.NoError(t, err)
	assert.Equal(t, int64(10000), result.CachedBalance)
	assert.Equal(t, int64(9500), result.DerivedBalance)
	assert.False(t, result.IsConsistent)
}

func TestGetBalance_EmptyID(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.GetBalance(context.Background(), "")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetTransactions_Success(t *testing.T) {
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
}

func TestGetTransactions_EmptyID(t *testing.T) {
	_, service := newTestService(t)

	result, err := service.GetTransactions(context.Background(), "", 50, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetTransactions_AccountNotFound(t *testing.T) {
	mockRepo, service := newTestService(t)

	mockRepo.EXPECT().GetAccountByID(gomock.Any(), "nonexistent").Return(nil, NewAccountNotFoundError())

	result, err := service.GetTransactions(context.Background(), "nonexistent", 50, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestReconcile_AllConsistent(t *testing.T) {
	mockRepo, service := newTestService(t)

	results := []AccountReconciliation{
		{AccountID: "acc-1", CachedBalance: 10000, DerivedBalance: 10000, IsConsistent: true},
		{AccountID: "acc-2", CachedBalance: 5000, DerivedBalance: 5000, IsConsistent: true},
	}

	mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(results, nil)

	resp, err := service.Reconcile(context.Background())
	assert.NoError(t, err)
	assert.True(t, resp.AllConsistent)
	assert.Len(t, resp.Accounts, 2)
}

func TestReconcile_WithInconsistency(t *testing.T) {
	mockRepo, service := newTestService(t)

	results := []AccountReconciliation{
		{AccountID: "acc-1", CachedBalance: 10000, DerivedBalance: 10000, IsConsistent: true},
		{AccountID: "acc-2", CachedBalance: 5000, DerivedBalance: 4500, IsConsistent: false},
	}

	mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(results, nil)

	resp, err := service.Reconcile(context.Background())
	assert.NoError(t, err)
	assert.False(t, resp.AllConsistent)
}

func TestReconcile_RepositoryError(t *testing.T) {
	mockRepo, service := newTestService(t)

	mockRepo.EXPECT().GetAllAccountsForReconciliation(gomock.Any()).Return(nil, apperrors.NewDatabaseError("db error", nil))

	resp, err := service.Reconcile(context.Background())
	assert.Error(t, err)
	assert.Nil(t, resp)
}
