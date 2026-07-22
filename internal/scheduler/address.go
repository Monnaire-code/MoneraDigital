package scheduler

import (
	"context"
	"time"

	"monera-digital/internal/adaptiveschedule"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository"
)

type AddressScheduler struct {
	repo repository.Address
}

func NewAddressScheduler(repo repository.Address) *AddressScheduler {
	return &AddressScheduler{repo: repo}
}

func (s *AddressScheduler) Start(ctx context.Context) {
	logger.Info("[AddressScheduler] Started - adaptive frozen-address maintenance")

	loop, err := adaptiveschedule.New(adaptiveschedule.Config{
		Name:    "address-unfreeze",
		MinIdle: 5 * time.Minute,
		MaxIdle: adaptiveschedule.DefaultMaxIdle,
	}, func(ctx context.Context) (adaptiveschedule.CycleOutcome, error) {
		count, err := s.repo.UnfreezeExpiredAddresses(ctx)
		if err != nil {
			logger.Error("[AddressScheduler] Failed to unfreeze addresses", "error", err.Error())
			return adaptiveschedule.CycleOutcome{}, err
		}
		if count > 0 {
			logger.Info("[AddressScheduler] Unfroze addresses", "count", count)
			return adaptiveschedule.CycleOutcome{Worked: true}, nil
		}
		return adaptiveschedule.CycleOutcome{}, nil
	})
	if err != nil {
		logger.Error("[AddressScheduler] adaptive schedule disabled", "error", err.Error())
		return
	}
	loop.Run(ctx)
	logger.Info("[AddressScheduler] Context cancelled, stopping...")
}
