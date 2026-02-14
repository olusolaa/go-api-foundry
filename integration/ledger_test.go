package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/akeren/go-api-foundry/config"
	"github.com/akeren/go-api-foundry/config/router"
	"github.com/akeren/go-api-foundry/domain"
	"github.com/akeren/go-api-foundry/internal/log"
	"github.com/akeren/go-api-foundry/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type LedgerAPITestSuite struct {
	suite.Suite
	db        *gorm.DB
	server    *httptest.Server
	baseURL   string
	logger    *log.Logger
	appConfig *config.ApplicationConfig
}

func (s *LedgerAPITestSuite) SetupSuite() {
	var err error
	s.db, err = gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=10000"), &gorm.Config{})
	s.Require().NoError(err)

	// SQLite serializes writes at the database level. Limiting to one open
	// connection prevents "database is locked" errors under concurrent load.
	sqlDB, err := s.db.DB()
	s.Require().NoError(err)
	sqlDB.SetMaxOpenConns(1)

	err = s.db.AutoMigrate(&models.Account{}, &models.Transaction{}, &models.LedgerEntry{})
	s.Require().NoError(err)

	// Seed system account
	systemAccount := models.Account{
		ID:          models.SystemAccountID,
		Name:        "External Funding Source",
		AccountType: models.AccountTypeSystem,
		Currency:    "USD",
	}
	err = s.db.Create(&systemAccount).Error
	s.Require().NoError(err)

	s.logger = log.NewLoggerWithJSONOutput()

	s.appConfig = &config.ApplicationConfig{
		DB:     s.db,
		Logger: s.logger,
	}

	s.appConfig.RouterService = router.CreateRouterService(s.logger, nil, &router.RouterConfig{
		RateLimitRequests: 1000,
		RateLimitWindow:   time.Minute,
		RequestTimeout:    30 * time.Second,
	})

	domain.SetupCoreDomain(s.appConfig)

	s.server = httptest.NewServer(s.appConfig.RouterService.GetEngine())
	s.baseURL = s.server.URL
}

func (s *LedgerAPITestSuite) TearDownSuite() {
	if s.server != nil {
		s.server.Close()
	}
	if s.db != nil {
		sqlDB, _ := s.db.DB()
		sqlDB.Close()
	}
}

func (s *LedgerAPITestSuite) SetupTest() {
	// Clean ledger data between tests (keep system account)
	s.db.Exec("DELETE FROM ledger_entries")
	s.db.Exec("DELETE FROM transactions")
	s.db.Exec("DELETE FROM accounts WHERE id != ?", models.SystemAccountID)
	s.db.Model(&models.Account{}).Where("id = ?", models.SystemAccountID).Updates(map[string]any{
		"balance": 0,
		"version": 0,
	})
}

// Helper methods

func (s *LedgerAPITestSuite) createAccount(name string) map[string]any {
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, err := http.Post(s.baseURL+"/v1/ledger/accounts", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusCreated, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	return response["data"].(map[string]any)
}

func (s *LedgerAPITestSuite) deposit(accountID string, amount int64, key string) map[string]any {
	body, _ := json.Marshal(map[string]any{
		"amount":          amount,
		"idempotency_key": key,
		"description":     "test deposit",
	})
	url := fmt.Sprintf("%s/v1/ledger/accounts/%s/deposit", s.baseURL, accountID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	return response
}

func (s *LedgerAPITestSuite) withdraw(accountID string, amount int64, key string) map[string]any {
	body, _ := json.Marshal(map[string]any{
		"amount":          amount,
		"idempotency_key": key,
		"description":     "test withdrawal",
	})
	url := fmt.Sprintf("%s/v1/ledger/accounts/%s/withdraw", s.baseURL, accountID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	return response
}

func (s *LedgerAPITestSuite) entriesByType(entries []any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(entries))
	for _, e := range entries {
		entry := e.(map[string]any)
		result[entry["entry_type"].(string)] = entry
	}
	return result
}

// Tests

func (s *LedgerAPITestSuite) TestCreateAccount() {
	data := s.createAccount("Alice")

	s.Equal("Alice", data["name"])
	s.Equal("USER", data["account_type"])
	s.Equal("USD", data["currency"])
	s.Equal(float64(0), data["balance"])
	s.NotEmpty(data["id"])
}

func (s *LedgerAPITestSuite) TestGetAccount() {
	created := s.createAccount("Bob")
	accountID := created["id"].(string)

	resp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s", s.baseURL, accountID))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]any)
	s.Equal("Bob", data["name"])
}

func (s *LedgerAPITestSuite) TestGetAccountNotFound() {
	resp, err := http.Get(s.baseURL + "/v1/ledger/accounts/nonexistent-id")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusNotFound, resp.StatusCode)
}

func (s *LedgerAPITestSuite) TestDeposit() {
	account := s.createAccount("Charlie")
	accountID := account["id"].(string)

	response := s.deposit(accountID, 10000, "dep-001")

	s.Equal(float64(201), response["code"])
	data := response["data"].(map[string]any)
	s.Equal("DEPOSIT", data["transaction_type"])
	s.Equal(float64(10000), data["amount"])

	entries := data["entries"].([]any)
	s.Len(entries, 2)

	// Verify entry details: one DEBIT on system account, one CREDIT on user account
	entryMap := s.entriesByType(entries)
	debit := entryMap["DEBIT"]
	credit := entryMap["CREDIT"]

	s.Equal(models.SystemAccountID, debit["account_id"])
	s.Equal(float64(10000), debit["amount"])

	s.Equal(accountID, credit["account_id"])
	s.Equal(float64(10000), credit["amount"])
	s.Equal(float64(10000), credit["balance_after"])
}

func (s *LedgerAPITestSuite) TestWithdraw() {
	account := s.createAccount("Dana")
	accountID := account["id"].(string)

	// Deposit first
	s.deposit(accountID, 10000, "dep-002")

	// Withdraw
	response := s.withdraw(accountID, 3000, "wd-001")
	s.Equal(float64(201), response["code"])
	data := response["data"].(map[string]any)
	s.Equal("WITHDRAWAL", data["transaction_type"])

	// Verify entry details: DEBIT on user account, CREDIT on system account
	entries := data["entries"].([]any)
	s.Len(entries, 2)
	entryMap := s.entriesByType(entries)

	debit := entryMap["DEBIT"]
	s.Equal(accountID, debit["account_id"])
	s.Equal(float64(3000), debit["amount"])
	s.Equal(float64(7000), debit["balance_after"]) // 10000 - 3000

	credit := entryMap["CREDIT"]
	s.Equal(models.SystemAccountID, credit["account_id"])
	s.Equal(float64(3000), credit["amount"])
}

func (s *LedgerAPITestSuite) TestWithdrawInsufficientFunds() {
	account := s.createAccount("Eve")
	accountID := account["id"].(string)

	// Deposit 5000
	s.deposit(accountID, 5000, "dep-003")

	// Try to withdraw 10000
	response := s.withdraw(accountID, 10000, "wd-002")
	s.Equal(float64(400), response["code"])
	s.Contains(response["message"], "insufficient funds")
}

func (s *LedgerAPITestSuite) TestTransfer() {
	alice := s.createAccount("Alice")
	bob := s.createAccount("Bob")
	aliceID := alice["id"].(string)
	bobID := bob["id"].(string)

	// Deposit to Alice
	s.deposit(aliceID, 10000, "dep-004")

	// Transfer Alice -> Bob
	body, _ := json.Marshal(map[string]any{
		"source_account_id": aliceID,
		"dest_account_id":   bobID,
		"amount":            4000,
		"idempotency_key":   "xfr-001",
		"description":       "test transfer",
	})
	resp, err := http.Post(s.baseURL+"/v1/ledger/transfers", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusCreated, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]any)
	s.Equal("TRANSFER", data["transaction_type"])
	s.Equal(float64(4000), data["amount"])

	// Verify entry details: DEBIT on Alice, CREDIT on Bob
	entries := data["entries"].([]any)
	s.Len(entries, 2)
	entryMap := s.entriesByType(entries)

	debit := entryMap["DEBIT"]
	s.Equal(aliceID, debit["account_id"])
	s.Equal(float64(4000), debit["amount"])
	s.Equal(float64(6000), debit["balance_after"]) // 10000 - 4000

	credit := entryMap["CREDIT"]
	s.Equal(bobID, credit["account_id"])
	s.Equal(float64(4000), credit["amount"])
	s.Equal(float64(4000), credit["balance_after"]) // 0 + 4000
}

func (s *LedgerAPITestSuite) TestIdempotency() {
	account := s.createAccount("Frank")
	accountID := account["id"].(string)

	// Deposit twice with same idempotency key
	resp1 := s.deposit(accountID, 5000, "dep-idempotent")
	resp2 := s.deposit(accountID, 5000, "dep-idempotent")

	// Both should succeed
	s.Equal(float64(201), resp1["code"])
	s.Equal(float64(201), resp2["code"])

	// But balance should only be 5000, not 10000
	balanceResp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, accountID))
	s.Require().NoError(err)
	defer balanceResp.Body.Close()

	var balanceResponse map[string]any
	json.NewDecoder(balanceResp.Body).Decode(&balanceResponse)
	data := balanceResponse["data"].(map[string]any)
	s.Equal(float64(5000), data["cached_balance"])
	s.Equal(float64(5000), data["derived_balance"])
	s.Equal(true, data["is_consistent"])
}

func (s *LedgerAPITestSuite) TestIdempotencyMismatch() {
	account := s.createAccount("IdempMismatch")
	accountID := account["id"].(string)

	// First deposit: $50 with key "dep-mismatch"
	resp1 := s.deposit(accountID, 5000, "dep-mismatch")
	s.Equal(float64(201), resp1["code"])

	// Second deposit: DIFFERENT amount ($100) with SAME key — must be rejected
	resp2 := s.deposit(accountID, 10000, "dep-mismatch")
	s.Equal(float64(409), resp2["code"])
	s.Contains(resp2["message"], "idempotency key already used")

	// Balance should still be 5000 (only the first deposit counted)
	balanceResp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, accountID))
	s.Require().NoError(err)
	defer balanceResp.Body.Close()

	var balanceResponse map[string]any
	json.NewDecoder(balanceResp.Body).Decode(&balanceResponse)
	data := balanceResponse["data"].(map[string]any)
	s.Equal(float64(5000), data["cached_balance"])
}

func (s *LedgerAPITestSuite) TestGetBalance() {
	account := s.createAccount("Grace")
	accountID := account["id"].(string)

	s.deposit(accountID, 10000, "dep-005")
	s.withdraw(accountID, 3000, "wd-003")

	resp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, accountID))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]any)
	s.Equal(float64(7000), data["cached_balance"])
	s.Equal(float64(7000), data["derived_balance"])
	s.Equal(true, data["is_consistent"])
}

func (s *LedgerAPITestSuite) TestGetTransactions() {
	account := s.createAccount("Heidi")
	accountID := account["id"].(string)

	s.deposit(accountID, 5000, "dep-006")
	s.deposit(accountID, 3000, "dep-007")

	resp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/transactions", s.baseURL, accountID))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].([]any)
	s.Len(data, 2)
}

func (s *LedgerAPITestSuite) TestReconciliation() {
	account := s.createAccount("Ivan")
	accountID := account["id"].(string)

	s.deposit(accountID, 10000, "dep-008")
	s.withdraw(accountID, 2000, "wd-004")

	resp, err := http.Get(s.baseURL + "/v1/ledger/reconciliation")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]any)
	s.Equal(true, data["all_consistent"])
	s.Equal(true, data["ledger_balanced"])
	s.Equal(data["total_debits"], data["total_credits"])
}

func (s *LedgerAPITestSuite) TestFullFlow() {
	// Create two accounts
	alice := s.createAccount("Alice")
	bob := s.createAccount("Bob")
	aliceID := alice["id"].(string)
	bobID := bob["id"].(string)

	// Deposit to Alice: $100.00
	s.deposit(aliceID, 10000, "dep-flow-1")

	// Transfer $40.00 from Alice to Bob
	body, _ := json.Marshal(map[string]any{
		"source_account_id": aliceID,
		"dest_account_id":   bobID,
		"amount":            4000,
		"idempotency_key":   "xfr-flow-1",
	})
	resp, err := http.Post(s.baseURL+"/v1/ledger/transfers", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusCreated, resp.StatusCode)

	// Withdraw $20.00 from Bob
	s.withdraw(bobID, 2000, "wd-flow-1")

	// Verify Alice balance: $60.00
	aliceBalance, _ := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, aliceID))
	var aliceResp map[string]any
	json.NewDecoder(aliceBalance.Body).Decode(&aliceResp)
	aliceBalance.Body.Close()
	aliceData := aliceResp["data"].(map[string]any)
	s.Equal(float64(6000), aliceData["cached_balance"])
	s.Equal(true, aliceData["is_consistent"])

	// Verify Bob balance: $20.00
	bobBalance, _ := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, bobID))
	var bobResp map[string]any
	json.NewDecoder(bobBalance.Body).Decode(&bobResp)
	bobBalance.Body.Close()
	bobData := bobResp["data"].(map[string]any)
	s.Equal(float64(2000), bobData["cached_balance"])
	s.Equal(true, bobData["is_consistent"])

	// Run reconciliation
	reconcileResp, _ := http.Get(s.baseURL + "/v1/ledger/reconciliation")
	var reconcile map[string]any
	json.NewDecoder(reconcileResp.Body).Decode(&reconcile)
	reconcileResp.Body.Close()
	s.Equal(true, reconcile["data"].(map[string]any)["all_consistent"])
}

func (s *LedgerAPITestSuite) TestConcurrentDeposits() {
	account := s.createAccount("ConcurrentUser")
	accountID := account["id"].(string)

	const goroutines = 10
	const depositAmount = int64(1000)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("concurrent-dep-%d", i)
			resp := s.deposit(accountID, depositAmount, key)
			assert.Equal(s.T(), float64(201), resp["code"])
		}()
	}

	wg.Wait()

	// Verify final balance equals sum of all deposits
	expectedBalance := float64(goroutines * depositAmount)

	resp, err := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, accountID))
	s.Require().NoError(err)
	defer resp.Body.Close()

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]any)
	s.Equal(expectedBalance, data["cached_balance"])
	s.Equal(expectedBalance, data["derived_balance"])
	s.Equal(true, data["is_consistent"])
}

func (s *LedgerAPITestSuite) TestConcurrentTransfers() {
	alice := s.createAccount("ConcAlice")
	bob := s.createAccount("ConcBob")
	aliceID := alice["id"].(string)
	bobID := bob["id"].(string)

	// Deposit enough to Alice
	s.deposit(aliceID, 100000, "conc-xfr-seed")

	const goroutines = 10
	const transferAmount = int64(1000)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"source_account_id": aliceID,
				"dest_account_id":   bobID,
				"amount":            transferAmount,
				"idempotency_key":   fmt.Sprintf("conc-xfr-%d", i),
			})
			resp, err := http.Post(s.baseURL+"/v1/ledger/transfers", "application/json", bytes.NewBuffer(body))
			assert.NoError(s.T(), err)
			resp.Body.Close()
			assert.Equal(s.T(), http.StatusCreated, resp.StatusCode)
		}()
	}

	wg.Wait()

	// Verify balances: Alice = 100000 - (10 * 1000) = 90000, Bob = 10 * 1000 = 10000
	aliceResp, _ := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, aliceID))
	var aliceBalance map[string]any
	json.NewDecoder(aliceResp.Body).Decode(&aliceBalance)
	aliceResp.Body.Close()
	aliceData := aliceBalance["data"].(map[string]any)
	s.Equal(float64(90000), aliceData["cached_balance"])
	s.Equal(true, aliceData["is_consistent"])

	bobResp, _ := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, bobID))
	var bobBalance map[string]any
	json.NewDecoder(bobResp.Body).Decode(&bobBalance)
	bobResp.Body.Close()
	bobData := bobBalance["data"].(map[string]any)
	s.Equal(float64(10000), bobData["cached_balance"])
	s.Equal(true, bobData["is_consistent"])
}

func (s *LedgerAPITestSuite) TestDepositToSystemAccountRejected() {
	// Attempting to deposit to the system account should be rejected
	response := s.deposit(models.SystemAccountID, 10000, "dep-sys-attack")
	s.Equal(float64(400), response["code"])
	s.Contains(response["message"], "system account")
}

func (s *LedgerAPITestSuite) TestWithdrawFromSystemAccountRejected() {
	// Attempting to withdraw from the system account should be rejected
	response := s.withdraw(models.SystemAccountID, 10000, "wd-sys-attack")
	s.Equal(float64(400), response["code"])
	s.Contains(response["message"], "system account")
}

func (s *LedgerAPITestSuite) TestTransferFromSystemAccountRejected() {
	bob := s.createAccount("BobTarget")
	bobID := bob["id"].(string)

	// Attempting to transfer FROM the system account = printing money
	body, _ := json.Marshal(map[string]any{
		"source_account_id": models.SystemAccountID,
		"dest_account_id":   bobID,
		"amount":            999999,
		"idempotency_key":   "xfr-sys-attack",
	})
	resp, err := http.Post(s.baseURL+"/v1/ledger/transfers", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusBadRequest, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	s.Contains(response["message"], "system account")
}

func (s *LedgerAPITestSuite) TestTransferToSystemAccountRejected() {
	alice := s.createAccount("AliceSource")
	aliceID := alice["id"].(string)
	s.deposit(aliceID, 10000, "dep-xfr-sys-2")

	// Attempting to transfer TO the system account should be rejected
	body, _ := json.Marshal(map[string]any{
		"source_account_id": aliceID,
		"dest_account_id":   models.SystemAccountID,
		"amount":            1000,
		"idempotency_key":   "xfr-sys-to-attack",
	})
	resp, err := http.Post(s.baseURL+"/v1/ledger/transfers", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusBadRequest, resp.StatusCode)

	var response map[string]any
	json.NewDecoder(resp.Body).Decode(&response)
	s.Contains(response["message"], "system account")
}

func (s *LedgerAPITestSuite) TestCreateAccountValidationError() {
	body, _ := json.Marshal(map[string]string{})
	resp, err := http.Post(s.baseURL+"/v1/ledger/accounts", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusBadRequest, resp.StatusCode)
}

func TestLedgerAPISuite(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "true" {
		t.Skip("Skipping integration tests. Set RUN_INTEGRATION_TESTS=true to run them")
	}

	suite.Run(t, new(LedgerAPITestSuite))
}
