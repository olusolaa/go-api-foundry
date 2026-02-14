package domain

import (
	"github.com/akeren/go-api-foundry/config"
	"github.com/akeren/go-api-foundry/domain/ledger"
	"github.com/akeren/go-api-foundry/domain/monitoring"
)

func SetupCoreDomain(appConfig *config.ApplicationConfig) {
	monitoringFactory := monitoring.NewMonitoringControllerFactory(appConfig.DB, appConfig.Logger, appConfig.Cache)
	appConfig.RouterService.MountController(monitoringFactory.CreateController())

	ledgerFactory := ledger.NewLedgerServiceFactory(appConfig.DB, appConfig.Logger)
	appConfig.RouterService.MountController(ledgerFactory.CreateController())
}
