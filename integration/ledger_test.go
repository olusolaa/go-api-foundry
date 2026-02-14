package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/akeren/go-api-foundry/config"
	"github.com/akeren/go-api-foundry/config/router"
	"github.com/akeren/go-api-foundry/domain"
	"github.com/akeren/go-api-foundry/internal/log"
	"github.com/akeren/go-api-foundry/internal/models"
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
	s.db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	s.Require().NoError(err)

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
	s.db.Model(&models.Account{}).Where("id = ?", models.SystemAccountID).Updates(map[string]interface{}{
		"balance": 0,
		"version": 0,
	})
}

// Helper methods

func (s *LedgerAPITestSuite) createAccount(name string) map[string]interface{} {
	body, _ := json.Marshal(map[string]string{"name": name})
	resp, err := http.Post(s.baseURL+"/v1/ledger/accounts", "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusCreated, resp.StatusCode)

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	return response["data"].(map[string]interface{})
}

func (s *LedgerAPITestSuite) deposit(accountID string, amount int64, key string) map[string]interface{} {
	body, _ := json.Marshal(map[string]interface{}{
		"amount":          amount,
		"idempotency_key": key,
		"description":     "test deposit",
	})
	url := fmt.Sprintf("%s/v1/ledger/accounts/%s/deposit", s.baseURL, accountID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	return response
}

func (s *LedgerAPITestSuite) withdraw(accountID string, amount int64, key string) map[string]interface{} {
	body, _ := json.Marshal(map[string]interface{}{
		"amount":          amount,
		"idempotency_key": key,
		"description":     "test withdrawal",
	})
	url := fmt.Sprintf("%s/v1/ledger/accounts/%s/withdraw", s.baseURL, accountID)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	s.Require().NoError(err)
	defer resp.Body.Close()

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	return response
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

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]interface{})
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
	data := response["data"].(map[string]interface{})
	s.Equal("DEPOSIT", data["transaction_type"])
	s.Equal(float64(10000), data["amount"])

	entries := data["entries"].([]interface{})
	s.Len(entries, 2)
}

func (s *LedgerAPITestSuite) TestWithdraw() {
	account := s.createAccount("Dana")
	accountID := account["id"].(string)

	// Deposit first
	s.deposit(accountID, 10000, "dep-002")

	// Withdraw
	response := s.withdraw(accountID, 3000, "wd-001")
	s.Equal(float64(201), response["code"])
	data := response["data"].(map[string]interface{})
	s.Equal("WITHDRAWAL", data["transaction_type"])
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
	body, _ := json.Marshal(map[string]interface{}{
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

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]interface{})
	s.Equal("TRANSFER", data["transaction_type"])
	s.Equal(float64(4000), data["amount"])
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

	var balanceResponse map[string]interface{}
	json.NewDecoder(balanceResp.Body).Decode(&balanceResponse)
	data := balanceResponse["data"].(map[string]interface{})
	s.Equal(float64(5000), data["cached_balance"])
	s.Equal(float64(5000), data["derived_balance"])
	s.Equal(true, data["is_consistent"])
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

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]interface{})
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

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].([]interface{})
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

	var response map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&response)
	data := response["data"].(map[string]interface{})
	s.Equal(true, data["all_consistent"])
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
	body, _ := json.Marshal(map[string]interface{}{
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
	var aliceResp map[string]interface{}
	json.NewDecoder(aliceBalance.Body).Decode(&aliceResp)
	aliceBalance.Body.Close()
	aliceData := aliceResp["data"].(map[string]interface{})
	s.Equal(float64(6000), aliceData["cached_balance"])
	s.Equal(true, aliceData["is_consistent"])

	// Verify Bob balance: $20.00
	bobBalance, _ := http.Get(fmt.Sprintf("%s/v1/ledger/accounts/%s/balance", s.baseURL, bobID))
	var bobResp map[string]interface{}
	json.NewDecoder(bobBalance.Body).Decode(&bobResp)
	bobBalance.Body.Close()
	bobData := bobResp["data"].(map[string]interface{})
	s.Equal(float64(2000), bobData["cached_balance"])
	s.Equal(true, bobData["is_consistent"])

	// Run reconciliation
	reconcileResp, _ := http.Get(s.baseURL + "/v1/ledger/reconciliation")
	var reconcile map[string]interface{}
	json.NewDecoder(reconcileResp.Body).Decode(&reconcile)
	reconcileResp.Body.Close()
	s.Equal(true, reconcile["data"].(map[string]interface{})["all_consistent"])
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
