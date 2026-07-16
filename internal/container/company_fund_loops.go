package container

import (
	"context"
	"log"
	"sync"
	"time"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/handlers"
)

type companyFundCurrentRateRefresher interface {
	Refresh(context.Context) (companyfund.CoinGeckoCurrentRateRefreshResult, error)
}

type companyFundCurrentValuationSweeper interface {
	Sweep(context.Context, int) companyfund.CompanyFundValuationSweepResult
}

func newCompanyFundSafeheronCollector(
	c *Container,
	normalizer *companyfund.SafeheronProviderEventNormalizer,
	eligibility companyfund.SafeheronWebhookEligibility,
) *companyfund.SafeheronProviderEventCollector {
	if c == nil || normalizer == nil || eligibility == nil || c.SafeheronWebhookHandler == nil || c.DepositEventRepo == nil || c.CompanyFundRepository == nil || c.CompanyFundAccountRegistry == nil {
		return nil
	}
	if _, ok := c.DepositEventRepo.(handlers.SafeheronEventSourceLookup); !ok {
		return nil
	}
	collector, err := companyfund.NewSafeheronProviderEventCollector(
		companyfund.NewPostgresSafeheronRawEventCandidateReader(c.DB, c.CompanyFundAccountRegistry),
		c.CompanyFundRepository,
		eligibility,
	)
	if err != nil {
		log.Printf("company-fund Safeheron raw-event collector disabled: configuration is invalid")
		return nil
	}
	return collector
}

func startCompanyFundCoreLoops(
	c *Container,
	config companyFundRuntimeConfig,
	runtime *companyfund.CompanyFundRuntime,
	refresher *companyfund.CoinGeckoCurrentRateRefresher,
	valuator *companyfund.CompanyFundCurrentValuator,
) {
	if c == nil || !config.StartBackgroundWorkers {
		return
	}
	ctx := c.companyFundRuntimeContext
	if ctx == nil {
		ctx = context.Background()
	}
	if c.CompanyFundAccountRegistry != nil {
		c.CompanyFundAccountRegistry.Start(ctx)
	}
	if c.CompanyFundSafeheronCoinCatalog != nil {
		c.CompanyFundSafeheronCoinCatalog.Start(ctx)
	}
	if runtime != nil {
		runtime.Start(ctx)
	}
	startCompanyFundAuxiliaryLoops(c, ctx, config, c.CompanyFundSafeheronCollector, refresher, valuator)
}

func startCompanyFundAuxiliaryLoops(
	c *Container,
	parent context.Context,
	config companyFundRuntimeConfig,
	collector *companyfund.SafeheronProviderEventCollector,
	refresher *companyfund.CoinGeckoCurrentRateRefresher,
	valuator *companyfund.CompanyFundCurrentValuator,
) {
	if c == nil || c.companyFundAuxDone != nil || (collector == nil && refresher == nil && valuator == nil) {
		return
	}
	collectorInterval, collectorIntervalErr := companyFundPositiveDuration(
		config.SafeheronCollectorInterval,
		defaultCompanyFundSafeheronCollectorInterval,
	)
	collectorBatch := companyFundPositiveIntOrDefault(
		config.SafeheronCollectorBatchSize,
		defaultCompanyFundSafeheronCollectorBatchSize,
	)
	refreshInterval, refreshIntervalErr := companyFundPositiveDuration(
		config.CurrentRateRefreshInterval,
		defaultCompanyFundCurrentRateRefreshInterval,
	)
	valuationInterval, valuationIntervalErr := companyFundPositiveDuration(
		config.CurrentValuationSweepInterval,
		defaultCompanyFundCurrentValuationSweepInterval,
	)
	valuationBatch := companyFundPositiveIntOrDefault(
		config.CurrentValuationSweepBatch,
		defaultCompanyFundCurrentValuationSweepBatch,
	)
	if collector != nil && (collectorIntervalErr != nil || collectorBatch < 1 || collectorBatch > maxCompanyFundSafeheronCollectorBatchSize) {
		log.Printf("company-fund Safeheron raw-event collector disabled: interval or batch configuration is invalid")
		collector = nil
	}
	if refresher != nil && refreshIntervalErr != nil {
		log.Printf("company-fund current USD rate refresh loop disabled: interval configuration is invalid")
		refresher = nil
	}
	if valuator != nil && (valuationIntervalErr != nil || valuationBatch < 1 || valuationBatch > maxCompanyFundCurrentValuationSweepBatch) {
		log.Printf("company-fund USD valuation repair sweep disabled: interval or batch configuration is invalid")
		valuator = nil
	}
	if collector == nil && refresher == nil && valuator == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	c.companyFundAuxCancel = cancel
	c.companyFundAuxDone = done

	var waitGroup sync.WaitGroup
	if collector != nil {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			runCompanyFundSafeheronCollector(ctx, collector, collectorInterval, collectorBatch)
		}()
	}
	if refresher != nil {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			runCompanyFundCurrentRateRefreshLoop(ctx, refresher, refreshInterval)
		}()
	}
	if valuator != nil {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			runCompanyFundCurrentValuationSweepLoop(ctx, valuator, valuationInterval, valuationBatch)
		}()
	}
	go func() {
		waitGroup.Wait()
		close(done)
	}()
}

func runCompanyFundSafeheronCollector(
	ctx context.Context,
	collector *companyfund.SafeheronProviderEventCollector,
	interval time.Duration,
	batchSize int,
) {
	collect := func() {
		if _, err := collector.Collect(ctx, batchSize); err != nil && ctx.Err() == nil {
			// The collector error can wrap source payload/database details; keep
			// process logs metadata-only and let the next bounded tick repair it.
			log.Printf("company-fund Safeheron raw-event collector failed")
		}
	}
	collect()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}

// runCompanyFundCurrentRateRefreshLoop owns only provider/cache refresh. It
// is intentionally separate from valuation repair: a faster sweep must not
// multiply CoinGecko provider calls.
func runCompanyFundCurrentRateRefreshLoop(
	ctx context.Context,
	refresher companyFundCurrentRateRefresher,
	interval time.Duration,
) {
	refresh := func() {
		result, err := refresher.Refresh(ctx)
		if result.RestoreFailed && ctx.Err() == nil {
			log.Printf("company-fund current USD rate snapshot restore failed")
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("company-fund current USD rate refresh failed")
		}
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// runCompanyFundCurrentValuationSweepLoop owns only database valuation repair
// over the latest cache. It never calls a rate provider; freshness is handled
// exclusively by runCompanyFundCurrentRateRefreshLoop.
func runCompanyFundCurrentValuationSweepLoop(
	ctx context.Context,
	valuator companyFundCurrentValuationSweeper,
	interval time.Duration,
	batchSize int,
) {
	sweep := func() {
		result := valuator.Sweep(ctx, batchSize)
		if result.Err != nil && ctx.Err() == nil {
			// Valuation repair is best-effort and must never affect provider-event
			// retry/finalization semantics.
			log.Printf("company-fund USD valuation repair sweep failed")
		}
	}
	sweep()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func stopCompanyFundAuxiliaryLoops(c *Container) {
	if c == nil {
		return
	}
	if c.companyFundAuxCancel != nil {
		c.companyFundAuxCancel()
	}
	if c.companyFundAuxDone != nil {
		<-c.companyFundAuxDone
	}
}
