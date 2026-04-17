package scheduler

import (
	"context"
	"time"

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
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	logger.Info("[AddressScheduler] Started - checking frozen addresses every 5 minutes")

	for {
		select {
		case <-ctx.Done():
			logger.Info("[AddressScheduler] Context cancelled, stopping...")
			return
		case <-ticker.C:
			s.unfreezeAddresses(ctx)
		}
	}
}

func (s *AddressScheduler) unfreezeAddresses(ctx context.Context) {
	count, err := s.repo.UnfreezeExpiredAddresses(ctx)
	if err != nil {
		logger.Error("[AddressScheduler] Failed to unfreeze addresses",
			"error", err.Error())
		return
	}

	if count > 0 {
		logger.Info("[AddressScheduler] Unfroze addresses",
			"count", count)
	}
}
