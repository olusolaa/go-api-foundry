package ledger

import (
	"github.com/akeren/go-api-foundry/config/router"
	"github.com/akeren/go-api-foundry/internal/log"
	"gorm.io/gorm"
)

type LedgerServiceFactory interface {
	CreateService() LedgerService
	CreateController() *router.RESTController
}

type DefaultLedgerServiceFactory struct {
	db     *gorm.DB
	logger *log.Logger
}

func NewLedgerServiceFactory(db *gorm.DB, logger *log.Logger) LedgerServiceFactory {
	return &DefaultLedgerServiceFactory{
		db:     db,
		logger: logger,
	}
}

func (f *DefaultLedgerServiceFactory) CreateService() LedgerService {
	repository := NewLedgerRepository(f.db)
	return NewLedgerService(f.logger, repository)
}

func (f *DefaultLedgerServiceFactory) CreateController() *router.RESTController {
	return NewLedgerController(f.db, f.logger)
}
