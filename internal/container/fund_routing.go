package container

import (
	"context"
	"log"

	"monera-digital/internal/fundrouting"
)

func finalizeSafeheronRouting(c *Container) {
	if c == nil || c.DB == nil || c.SafeheronRoutingMode == "" {
		return
	}
	log.Printf("Safeheron transaction routing mode=%s legacy_bridge_instances=0 legacy_collector_instances=0 legacy_deposit_transaction_claimers=0", c.SafeheronRoutingMode)
	if c.SafeheronRoutingMode == fundrouting.ModeCaptureOnly {
		log.Printf("Safeheron transaction routing capture-only: transaction events remain PENDING")
		return
	}
	if !c.companyFundRuntimeConfig.StartBackgroundWorkers {
		log.Printf("Safeheron routing workers disabled by COMPANY_FUND_START_BACKGROUND_WORKERS=false")
		return
	}
	repository := fundrouting.NewRepository(c.DB)
	resolver := fundrouting.NewCatalogNetworkResolver(c.CompanyFundSafeheronCoinCatalog)
	worker, err := fundrouting.NewWorker(repository, resolver)
	if err != nil {
		panic(err)
	}
	c.FundRoutingRepository = repository
	c.FundRoutingWorker = worker
	reconciler, reconcileErr := fundrouting.NewReconciler(c.DB)
	if reconcileErr != nil {
		panic(reconcileErr)
	}
	c.FundRoutingReconciler = reconciler
	escalator, escalationErr := fundrouting.NewAlertEscalator(c.DB)
	if escalationErr != nil {
		panic(escalationErr)
	}
	c.FundRoutingAlertEscalator = escalator
	ctx := c.safeheronRuntimeContext
	if ctx == nil {
		ctx = context.Background()
	}
	if c.AlertService != nil {
		notifier, notifierErr := fundrouting.NewAlertNotifier(c.DB, c.AlertService)
		if notifierErr != nil {
			panic(notifierErr)
		}
		c.FundRoutingAlertNotifier = notifier
	} else {
		log.Printf("Safeheron routing alert notifier disabled: no alert sinks configured")
	}
	if c.CompanyFundRepository != nil {
		projectionWorker, projectionErr := fundrouting.NewProjectionWorker(c.DB, c.CompanyFundRepository)
		if projectionErr != nil {
			panic(projectionErr)
		}
		c.FundRoutingProjectionWorker = projectionWorker
		if c.CompanyFundRuntime != nil {
			projectionWorker.SetOnProviderEventInserted(c.CompanyFundRuntime.ProviderEventWakeFunc())
		}
	}
	// Routing → projection wake after durable routing work in a cycle.
	if c.FundRoutingProjectionWorker != nil {
		worker.SetOnWorked(func() {
			_ = c.FundRoutingProjectionWorker.Notify()
			if c.FundRoutingAlertNotifier != nil {
				_ = c.FundRoutingAlertNotifier.Notify()
			}
		})
	}
	go worker.Run(ctx)
	go reconciler.Run(ctx)
	go escalator.Run(ctx)
	metricsMonitor, metricsErr := fundrouting.NewMetricsMonitor(c.DB)
	if metricsErr != nil {
		panic(metricsErr)
	}
	go metricsMonitor.Run(ctx)
	if c.FundRoutingAlertNotifier != nil {
		go c.FundRoutingAlertNotifier.Run(ctx)
	}
	if c.FundRoutingProjectionWorker != nil {
		go c.FundRoutingProjectionWorker.Run(ctx)
	}
	// Re-bind webhook wakes once routing workers exist so transaction events
	// advance both deposit and routing without fixed second-level polling.
	wireSafeheronWebhookWorkerWakes(c)
}

// wireSafeheronWebhookWorkerWakes attaches process-local wakes after durable
// Safeheron webhook persistence. Pointers are read on each call so partial
// container assembly remains safe.
func wireSafeheronWebhookWorkerWakes(c *Container) {
	if c == nil || c.SafeheronWebhookHandler == nil {
		return
	}
	c.SafeheronWebhookHandler.SetDepositWorkerWake(func() {
		if c.DepositWorker != nil {
			_ = c.DepositWorker.Notify()
		}
		if c.FundRoutingWorker != nil {
			_ = c.FundRoutingWorker.Notify()
		}
		if c.FundRoutingAlertNotifier != nil {
			_ = c.FundRoutingAlertNotifier.Notify()
		}
		if c.FundRoutingProjectionWorker != nil {
			_ = c.FundRoutingProjectionWorker.Notify()
		}
	})
}
