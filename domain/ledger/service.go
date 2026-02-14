package ledger

import (
	"context"

	"github.com/akeren/go-api-foundry/internal/log"
	"github.com/akeren/go-api-foundry/internal/models"
	apperrors "github.com/akeren/go-api-foundry/pkg/errors"
)

type LedgerService interface {
	CreateAccount(ctx context.Context, req *CreateAccountRequest) (*AccountResponse, error)
	GetAccount(ctx context.Context, id string) (*AccountResponse, error)
	Deposit(ctx context.Context, accountID string, req *DepositRequest) (*TransactionResponse, error)
	Withdraw(ctx context.Context, accountID string, req *WithdrawRequest) (*TransactionResponse, error)
	Transfer(ctx context.Context, req *TransferRequest) (*TransactionResponse, error)
	GetBalance(ctx context.Context, accountID string) (*BalanceResponse, error)
	GetTransactions(ctx context.Context, accountID string, limit, offset int) ([]TransactionResponse, error)
	Reconcile(ctx context.Context) (*ReconciliationResponse, error)
}

type ledgerService struct {
	logger     *log.Logger
	repository LedgerRepository
}

func NewLedgerService(logger *log.Logger, repository LedgerRepository) LedgerService {
	return &ledgerService{logger: logger, repository: repository}
}

func (s *ledgerService) CreateAccount(ctx context.Context, req *CreateAccountRequest) (*AccountResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if req == nil {
		logger.Error("CreateAccount received nil request")
		return nil, apperrors.NewInvalidRequestError("request cannot be nil", nil)
	}

	account := ToAccountModel(req)
	created, err := s.repository.CreateAccount(ctx, account)
	if err != nil {
		logger.Error("Failed to create account", "error", err)
		return nil, err
	}

	resp := ToAccountResponse(created)
	return &resp, nil
}

func (s *ledgerService) GetAccount(ctx context.Context, id string) (*AccountResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if id == "" {
		logger.Error("GetAccount received empty ID")
		return nil, apperrors.NewInvalidRequestError("account ID cannot be empty", nil)
	}

	account, err := s.repository.GetAccountByID(ctx, id)
	if err != nil {
		logger.Error("Failed to get account", "id", id, "error", err)
		return nil, err
	}

	resp := ToAccountResponse(account)
	return &resp, nil
}

func (s *ledgerService) Deposit(ctx context.Context, accountID string, req *DepositRequest) (*TransactionResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if req == nil {
		logger.Error("Deposit received nil request")
		return nil, apperrors.NewInvalidRequestError("request cannot be nil", nil)
	}

	if accountID == "" {
		logger.Error("Deposit received empty account ID")
		return nil, apperrors.NewInvalidRequestError("account ID cannot be empty", nil)
	}

	if req.Amount <= 0 {
		return nil, NewInvalidAmountError()
	}

	cmd := DoubleEntryCommand{
		SourceAccountID: models.SystemAccountID,
		DestAccountID:   accountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		TransactionType: models.TransactionTypeDeposit,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
	}

	txn, err := s.repository.ExecuteDoubleEntry(ctx, cmd)
	if err != nil {
		logger.Error("Failed to execute deposit", "account_id", accountID, "error", err)
		return nil, err
	}

	resp := ToTransactionResponse(txn)
	return &resp, nil
}

func (s *ledgerService) Withdraw(ctx context.Context, accountID string, req *WithdrawRequest) (*TransactionResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if req == nil {
		logger.Error("Withdraw received nil request")
		return nil, apperrors.NewInvalidRequestError("request cannot be nil", nil)
	}

	if accountID == "" {
		logger.Error("Withdraw received empty account ID")
		return nil, apperrors.NewInvalidRequestError("account ID cannot be empty", nil)
	}

	if req.Amount <= 0 {
		return nil, NewInvalidAmountError()
	}

	cmd := DoubleEntryCommand{
		SourceAccountID: accountID,
		DestAccountID:   models.SystemAccountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		TransactionType: models.TransactionTypeWithdrawal,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
	}

	txn, err := s.repository.ExecuteDoubleEntry(ctx, cmd)
	if err != nil {
		logger.Error("Failed to execute withdrawal", "account_id", accountID, "error", err)
		return nil, err
	}

	resp := ToTransactionResponse(txn)
	return &resp, nil
}

func (s *ledgerService) Transfer(ctx context.Context, req *TransferRequest) (*TransactionResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if req == nil {
		logger.Error("Transfer received nil request")
		return nil, apperrors.NewInvalidRequestError("request cannot be nil", nil)
	}

	if req.SourceAccountID == "" || req.DestAccountID == "" {
		logger.Error("Transfer received empty account IDs")
		return nil, apperrors.NewInvalidRequestError("source and destination account IDs are required", nil)
	}

	if req.SourceAccountID == req.DestAccountID {
		return nil, NewSelfTransferError()
	}

	if req.Amount <= 0 {
		return nil, NewInvalidAmountError()
	}

	cmd := DoubleEntryCommand{
		SourceAccountID: req.SourceAccountID,
		DestAccountID:   req.DestAccountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		TransactionType: models.TransactionTypeTransfer,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
	}

	txn, err := s.repository.ExecuteDoubleEntry(ctx, cmd)
	if err != nil {
		logger.Error("Failed to execute transfer", "error", err)
		return nil, err
	}

	resp := ToTransactionResponse(txn)
	return &resp, nil
}

func (s *ledgerService) GetBalance(ctx context.Context, accountID string) (*BalanceResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if accountID == "" {
		logger.Error("GetBalance received empty account ID")
		return nil, apperrors.NewInvalidRequestError("account ID cannot be empty", nil)
	}

	account, err := s.repository.GetAccountByID(ctx, accountID)
	if err != nil {
		logger.Error("Failed to get account for balance", "id", accountID, "error", err)
		return nil, err
	}

	derived, err := s.repository.GetDerivedBalance(ctx, accountID)
	if err != nil {
		logger.Error("Failed to get derived balance", "id", accountID, "error", err)
		return nil, err
	}

	return &BalanceResponse{
		AccountID:      account.ID,
		CachedBalance:  account.Balance,
		DerivedBalance: derived,
		Currency:       account.Currency,
		IsConsistent:   account.Balance == derived,
	}, nil
}

func (s *ledgerService) GetTransactions(ctx context.Context, accountID string, limit, offset int) ([]TransactionResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	if accountID == "" {
		logger.Error("GetTransactions received empty account ID")
		return nil, apperrors.NewInvalidRequestError("account ID cannot be empty", nil)
	}

	// Verify account exists
	if _, err := s.repository.GetAccountByID(ctx, accountID); err != nil {
		logger.Error("Failed to verify account for transactions", "id", accountID, "error", err)
		return nil, err
	}

	transactions, err := s.repository.GetTransactionsByAccountID(ctx, accountID, limit, offset)
	if err != nil {
		logger.Error("Failed to get transactions", "account_id", accountID, "error", err)
		return nil, err
	}

	responses := make([]TransactionResponse, 0, len(transactions))
	for _, txn := range transactions {
		responses = append(responses, ToTransactionResponse(&txn))
	}

	return responses, nil
}

func (s *ledgerService) Reconcile(ctx context.Context) (*ReconciliationResponse, error) {
	logger := log.GetLoggerInstanceFromContext(ctx, s.logger)

	results, err := s.repository.GetAllAccountsForReconciliation(ctx)
	if err != nil {
		logger.Error("Failed to run reconciliation", "error", err)
		return nil, err
	}

	allConsistent := true
	for _, r := range results {
		if !r.IsConsistent {
			allConsistent = false
			logger.Error("Reconciliation mismatch detected",
				"account_id", r.AccountID,
				"cached", r.CachedBalance,
				"derived", r.DerivedBalance,
			)
		}
	}

	return &ReconciliationResponse{
		Accounts:      results,
		AllConsistent: allConsistent,
	}, nil
}
