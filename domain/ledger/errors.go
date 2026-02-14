package ledger

import (
	apperrors "github.com/akeren/go-api-foundry/pkg/errors"
)

func NewInsufficientFundsError() *apperrors.AppError {
	return apperrors.NewInvalidRequestError("insufficient funds", nil)
}

func NewCurrencyMismatchError() *apperrors.AppError {
	return apperrors.NewInvalidRequestError("currency mismatch between accounts", nil)
}

func NewAccountNotFoundError() *apperrors.AppError {
	return apperrors.NewNotFoundError("account not found", nil)
}

func NewSelfTransferError() *apperrors.AppError {
	return apperrors.NewInvalidRequestError("cannot transfer to the same account", nil)
}

func NewInvalidAmountError() *apperrors.AppError {
	return apperrors.NewInvalidRequestError("amount must be greater than zero", nil)
}
