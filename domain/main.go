package domain

import (
	"github.com/akeren/go-api-foundry/config"
	"github.com/akeren/go-api-foundry/domain/ledger"
	"github.com/akeren/go-api-foundry/domain/monitoring"
)

func SetupCoreDomain(appConfig *config.ApplicationConfig) {
	appConfig.RouterService.MountController(monitoring.NewMonitoringController(appConfig.DB, appConfig.Logger, appConfig.Cache))
	appConfig.RouterService.MountController(ledger.NewLedgerController(appConfig.DB, appConfig.Logger))
}
