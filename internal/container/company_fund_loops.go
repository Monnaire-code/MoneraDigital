package container

import (
	"context"
	"log"
	"sync"
	"time"

	"monera-digital/internal/adaptiveschedule"
	"monera-digital/internal/companyfund"
	"monera-digital/internal/fundrouting"
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
	if c != nil && (c.SafeheronRoutingMode == fundrouting.ModeCaptureOnly || c.SafeheronRoutingMode == fundrouting.ModeRoutingAuthoritative) {
		return nil
	}
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
	startCompanyFundAuxiliaryLoops(c, ctx, config, c.CompanyFundSafeheronCollector, refresher, valuator)
	if runtime != nil {
		runtime.Start(ctx)
	}
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

	var valuationLoop *adaptiveschedule.Loop
	if valuator != nil {
		valuationLoop = newCompanyFundCurrentValuationSweepLoop(valuator, valuationInterval, valuationBatch)
		c.companyFundValuationLoop = valuationLoop
	}
	var rateRefreshLoop *adaptiveschedule.Loop
	if refresher != nil {
		rateRefreshLoop = newCompanyFundCurrentRateRefreshLoop(refresher, refreshInterval, func() {
			if valuationLoop != nil {
				_ = valuationLoop.Notify()
			}
		})
		c.companyFundRateRefreshLoop = rateRefreshLoop
	}

	var waitGroup sync.WaitGroup
	if collector != nil {
		runCompanyFundAuxTask(&waitGroup, "safeheron_collector", func() {
			runCompanyFundSafeheronCollector(ctx, collector, collectorInterval, collectorBatch, c)
		})
	}
	if rateRefreshLoop != nil {
		runCompanyFundAuxTask(&waitGroup, "rate_refresh", func() {
			rateRefreshLoop.Run(ctx)
		})
	}
	if valuationLoop != nil {
		runCompanyFundAuxTask(&waitGroup, "valuation_sweep", func() {
			valuationLoop.Run(ctx)
		})
	}
	go func() {
		defer func() {
			if recover() != nil {
				log.Printf("company-fund auxiliary panic recovered: kind=completion")
			}
		}()
		waitGroup.Wait()
		close(done)
	}()
}

func runCompanyFundAuxTask(waitGroup *sync.WaitGroup, kind string, run func()) {
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		defer func() {
			if recover() != nil {
				log.Printf("company-fund auxiliary panic recovered: kind=%s", kind)
			}
		}()
		run()
	}()
}

func companyFundAdaptiveMaxIdle(minIdle time.Duration) time.Duration {
	return adaptiveschedule.MaxIdleAtLeast(minIdle)
}

func runCompanyFundSafeheronCollector(
	ctx context.Context,
	collector *companyfund.SafeheronProviderEventCollector,
	minIdle time.Duration,
	batchSize int,
	c *Container,
) {
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "company-fund-safeheron-collector",
		MinIdle: minIdle,
		MaxIdle: companyFundAdaptiveMaxIdle(minIdle),
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		result, err := collector.Collect(ctx, batchSize)
		if err != nil && ctx.Err() == nil {
			log.Printf("company-fund Safeheron raw-event collector failed")
		}
		// Newly bridged inbox rows re-accelerate; empty compensation backs off.
		worked := result.Inserted > 0
		if worked && c != nil && c.CompanyFundRuntime != nil {
			_ = c.CompanyFundRuntime.NotifyProviderEvent()
		}
		return adaptiveschedule.CycleOutcome{
			Worked:   worked,
			MoreWork: worked && result.Inserted >= batchSize && batchSize > 0,
		}, err
	})
	if err != nil {
		log.Printf("company-fund Safeheron raw-event collector disabled: adaptive schedule invalid")
		return
	}
	loop.Run(ctx)
}

// runCompanyFundCurrentRateRefreshLoop owns only provider/cache refresh. It
// is intentionally separate from valuation repair: a faster sweep must not
// multiply CoinGecko provider calls.
func runCompanyFundCurrentRateRefreshLoop(
	ctx context.Context,
	refresher companyFundCurrentRateRefresher,
	minIdle time.Duration,
) {
	newCompanyFundCurrentRateRefreshLoop(refresher, minIdle, nil).Run(ctx)
}

func newCompanyFundCurrentRateRefreshLoop(
	refresher companyFundCurrentRateRefresher,
	minIdle time.Duration,
	onRefreshed func(),
) *adaptiveschedule.Loop {
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "company-fund-current-rate-refresh",
		MinIdle: minIdle,
		MaxIdle: companyFundAdaptiveMaxIdle(minIdle),
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		result, err := refresher.Refresh(ctx)
		if result.RestoreFailed && ctx.Err() == nil {
			log.Printf("company-fund current USD rate snapshot restore failed")
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("company-fund current USD rate refresh failed")
		}
		if err == nil && onRefreshed != nil {
			onRefreshed()
		}
		// Maintenance-only: never report Worked so idle can reach MaxIdle.
		return adaptiveschedule.CycleOutcome{}, err
	})
	if err != nil {
		log.Printf("company-fund current USD rate refresh loop disabled: adaptive schedule invalid")
		return nil
	}
	return loop
}

// runCompanyFundCurrentValuationSweepLoop owns only database valuation repair
// over the latest cache. It never calls a rate provider; freshness is handled
// exclusively by runCompanyFundCurrentRateRefreshLoop.
func runCompanyFundCurrentValuationSweepLoop(
	ctx context.Context,
	valuator companyFundCurrentValuationSweeper,
	minIdle time.Duration,
	batchSize int,
) {
	newCompanyFundCurrentValuationSweepLoop(valuator, minIdle, batchSize).Run(ctx)
}

func newCompanyFundCurrentValuationSweepLoop(
	valuator companyFundCurrentValuationSweeper,
	minIdle time.Duration,
	batchSize int,
) *adaptiveschedule.Loop {
	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "company-fund-valuation-sweep",
		MinIdle: minIdle,
		MaxIdle: companyFundAdaptiveMaxIdle(minIdle),
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		result := valuator.Sweep(ctx, batchSize)
		if result.Err != nil && ctx.Err() == nil {
			log.Printf("company-fund USD valuation repair sweep failed")
		}
		worked := result.Applied > 0
		return adaptiveschedule.CycleOutcome{
			Worked:   worked,
			MoreWork: worked && result.Applied >= batchSize && batchSize > 0,
		}, result.Err
	})
	if err != nil {
		log.Printf("company-fund USD valuation repair sweep disabled: adaptive schedule invalid")
		return nil
	}
	return loop
}

func notifyCompanyFundValuationWork(c *Container) {
	if c == nil {
		return
	}
	if c.companyFundRateRefreshLoop != nil {
		_ = c.companyFundRateRefreshLoop.Notify()
		return
	}
	if c.companyFundValuationLoop != nil {
		_ = c.companyFundValuationLoop.Notify()
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
