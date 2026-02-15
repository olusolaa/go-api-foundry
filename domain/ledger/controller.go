package ledger

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/akeren/go-api-foundry/config/router"
	"github.com/akeren/go-api-foundry/internal/log"
	apperrors "github.com/akeren/go-api-foundry/pkg/errors"
	"gorm.io/gorm"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 100
)

// mapDomainError translates domain sentinel errors into HTTP status codes
// and messages. Infrastructure errors (AppError) fall through to the
// existing pkg/errors mapping.
func mapDomainError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrAccountNotFound):
		return http.StatusNotFound, ErrAccountNotFound.Error()
	case errors.Is(err, ErrInsufficientFunds):
		return http.StatusBadRequest, ErrInsufficientFunds.Error()
	case errors.Is(err, ErrCurrencyMismatch):
		return http.StatusBadRequest, ErrCurrencyMismatch.Error()
	case errors.Is(err, ErrSelfTransfer):
		return http.StatusBadRequest, ErrSelfTransfer.Error()
	case errors.Is(err, ErrInvalidAmount):
		return http.StatusBadRequest, ErrInvalidAmount.Error()
	case errors.Is(err, ErrSystemAccountForbidden):
		return http.StatusBadRequest, ErrSystemAccountForbidden.Error()
	case errors.Is(err, ErrIdempotencyConflict):
		return http.StatusConflict, ErrIdempotencyConflict.Error()
	default:
		return apperrors.HTTPStatusCode(err), apperrors.GetHumanReadableMessage(err)
	}
}

func errorResult(err error) *router.ServiceResult {
	code, msg := mapDomainError(err)
	return router.ErrorResult(code, msg, nil)
}

func bindJSON[T any](ctx *router.RequestContext) (*T, *router.ServiceResult) {
	var req T
	if err := ctx.ShouldBindJSON(&req); err != nil {
		router.GetLogger(ctx).Error("Failed to bind request", "error", err)
		validationErrors := apperrors.FormatValidationErrors(err, &req)
		if len(validationErrors) > 0 {
			return nil, router.BadRequestResult("Invalid request payload", validationErrors)
		}
		return nil, router.BadRequestResult("Invalid request body", nil)
	}
	return &req, nil
}

func NewLedgerController(db *gorm.DB, logger *log.Logger) *router.RESTController {
	return router.NewVersionedRESTController(
		"LedgerController",
		"v1",
		"/ledger",
		func(rs *router.RouterService, c *router.RESTController) {
			repository := NewLedgerRepository(db)
			service := NewLedgerService(logger, repository)

			rs.AddPostHandler(c, nil, "/accounts", createAccountHandler(service))
			rs.AddGetHandler(c, nil, "/accounts/:id", getAccountHandler(service))
			rs.AddPostHandler(c, nil, "/accounts/:id/deposit", depositHandler(service))
			rs.AddPostHandler(c, nil, "/accounts/:id/withdraw", withdrawHandler(service))
			rs.AddPostHandler(c, nil, "/transfers", transferHandler(service))
			rs.AddGetHandler(c, nil, "/accounts/:id/balance", getBalanceHandler(service))
			rs.AddGetHandler(c, nil, "/accounts/:id/transactions", getTransactionsHandler(service))
			rs.AddGetHandler(c, nil, "/reconciliation", reconciliationHandler(service))
		},
	)
}

func createAccountHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		req, bindErr := bindJSON[CreateAccountRequest](ctx)
		if bindErr != nil {
			return bindErr
		}

		response, err := service.CreateAccount(ctx.Request.Context(), req)
		if err != nil {
			return errorResult(err)
		}

		return router.CreatedResult(response, "Account")
	}
}

func getAccountHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		id := ctx.Param("id")
		if id == "" {
			return router.BadRequestResult("Account ID is required", nil)
		}

		response, err := service.GetAccount(ctx.Request.Context(), id)
		if err != nil {
			return errorResult(err)
		}

		return router.OKResult(response, "Account retrieved successfully")
	}
}

func depositHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		id := ctx.Param("id")
		if id == "" {
			return router.BadRequestResult("Account ID is required", nil)
		}

		req, bindErr := bindJSON[DepositRequest](ctx)
		if bindErr != nil {
			return bindErr
		}

		response, err := service.Deposit(ctx.Request.Context(), id, req)
		if err != nil {
			return errorResult(err)
		}

		return router.CreatedResult(response, "Deposit")
	}
}

func withdrawHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		id := ctx.Param("id")
		if id == "" {
			return router.BadRequestResult("Account ID is required", nil)
		}

		req, bindErr := bindJSON[WithdrawRequest](ctx)
		if bindErr != nil {
			return bindErr
		}

		response, err := service.Withdraw(ctx.Request.Context(), id, req)
		if err != nil {
			return errorResult(err)
		}

		return router.CreatedResult(response, "Withdrawal")
	}
}

func transferHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		req, bindErr := bindJSON[TransferRequest](ctx)
		if bindErr != nil {
			return bindErr
		}

		response, err := service.Transfer(ctx.Request.Context(), req)
		if err != nil {
			return errorResult(err)
		}

		return router.CreatedResult(response, "Transfer")
	}
}

func getBalanceHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		id := ctx.Param("id")
		if id == "" {
			return router.BadRequestResult("Account ID is required", nil)
		}

		response, err := service.GetBalance(ctx.Request.Context(), id)
		if err != nil {
			return errorResult(err)
		}

		return router.OKResult(response, "Balance retrieved successfully")
	}
}

func getTransactionsHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		id := ctx.Param("id")
		if id == "" {
			return router.BadRequestResult("Account ID is required", nil)
		}

		limit := defaultPageLimit
		offset := 0

		if l := ctx.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= maxPageLimit {
				limit = parsed
			}
		}
		if o := ctx.Query("offset"); o != "" {
			if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
				offset = parsed
			}
		}

		response, err := service.GetTransactions(ctx.Request.Context(), id, limit, offset)
		if err != nil {
			return errorResult(err)
		}

		return router.OKResult(response, "Transactions retrieved successfully")
	}
}

func reconciliationHandler(service LedgerService) router.HandlerFunction {
	return func(ctx *router.RequestContext) *router.ServiceResult {
		response, err := service.Reconcile(ctx.Request.Context())
		if err != nil {
			return errorResult(err)
		}

		return router.OKResult(response, "Reconciliation completed successfully")
	}
}
