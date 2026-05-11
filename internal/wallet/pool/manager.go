package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
)

type AlertFunc func(level, title, message string)

var defaultRetryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}

type Manager struct {
	repo        Repository
	client      safeheron.SafeheronClient
	registry    *walletconfig.Registry
	mu          sync.RWMutex
	alertFn     AlertFunc
	retryDelays []time.Duration
}

func NewManager(repo Repository, client safeheron.SafeheronClient, registry *walletconfig.Registry) *Manager {
	return &Manager{
		repo:        repo,
		client:      client,
		registry:    registry,
		retryDelays: append([]time.Duration(nil), defaultRetryDelays...),
	}
}

func (m *Manager) SetAlertFunc(fn AlertFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertFn = fn
}

func (m *Manager) getAlertFn() AlertFunc {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.alertFn
}

func (m *Manager) GetOrAssign(ctx context.Context, userID int, networkFamily string) (*Address, error) {
	addr, err := m.repo.GetUserAddress(ctx, userID, networkFamily)
	if err == nil {
		return addr, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get user address: %w", err)
	}

	addr, err = m.repo.AssignAvailable(ctx, userID, networkFamily)
	if err == nil {
		return addr, nil
	}
	if !errors.Is(err, ErrPoolEmpty) {
		return nil, fmt.Errorf("assign: %w", err)
	}

	log.Printf("pool empty for %s, triggering sync replenish", networkFamily)
	if repErr := m.Replenish(ctx, networkFamily, emergencyReplenishCount); repErr != nil {
		return nil, fmt.Errorf("sync replenish failed: %w", repErr)
	}

	addr, err = m.repo.AssignAvailable(ctx, userID, networkFamily)
	if err != nil {
		return nil, fmt.Errorf("assign after replenish: %w", err)
	}
	return addr, nil
}

const (
	maxReplenishTarget      = 500
	emergencyReplenishCount = 10
	bulkInsertFlushSize     = 50
)

func (m *Manager) Replenish(ctx context.Context, networkFamily string, targetCount int) error {
	if targetCount > maxReplenishTarget {
		return fmt.Errorf("target count %d exceeds max %d", targetCount, maxReplenishTarget)
	}

	current, err := m.repo.CountByStatus(ctx, networkFamily, StatusAvailable)
	if err != nil {
		return fmt.Errorf("count available: %w", err)
	}

	needed := targetCount - current
	if needed <= 0 {
		return nil
	}

	log.Printf("pool replenish: %s %d→%d (creating %d)", networkFamily, current, targetCount, needed)

	coinKeys := m.registry.SafeheronCoinKeysByFamily(networkFamily)
	if len(coinKeys) == 0 {
		return fmt.Errorf("no coin keys for family %s", networkFamily)
	}

	var totalErrors int
	var firstErr error
	var batch []*Address

	for i := range needed {
		addr, err := m.createOneWallet(ctx, networkFamily, coinKeys)
		if err != nil {
			totalErrors++
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("pool create wallet failed (%s %d/%d): %v", networkFamily, i+1, needed, err)
			continue
		}
		batch = append(batch, addr)

		if len(batch) >= bulkInsertFlushSize || i+1 == needed {
			if err := m.repo.BulkInsert(ctx, batch); err != nil {
				return fmt.Errorf("bulk insert at %d: %w", i+1, err)
			}
			batch = batch[:0]
		}

		if (i+1)%10 == 0 || i+1 == needed {
			log.Printf("[%s] %d/%d done", networkFamily, i+1, needed)
		}
	}

	if totalErrors > 0 {
		return fmt.Errorf("replenish %s: %d/%d failed, first error: %w", networkFamily, totalErrors, needed, firstErr)
	}
	return nil
}

func (m *Manager) createOneWallet(ctx context.Context, networkFamily string, coinKeys []string) (*Address, error) {
	customerRefID := uuid.New().String()

	wallet, err := m.createWithRetry(ctx, customerRefID, coinKeys)
	if err != nil {
		return nil, err
	}

	addr := &Address{
		NetworkFamily:       networkFamily,
		SafeheronAccountKey: wallet.AccountKey,
		CustomerRefID:       customerRefID,
		AccountTag:          "DEPOSIT",
		HiddenOnUI:          true,
		Status:              StatusAvailable,
	}

	if len(wallet.CoinAddressList) > 0 {
		first := wallet.CoinAddressList[0]
		addr.Address = first.Address
		addr.AddressGroupKey = first.AddressGroupKey
		addr.DerivePath = first.DerivePath
	}

	return addr, nil
}

func (m *Manager) createWithRetry(ctx context.Context, customerRefID string, coinKeys []string) (*safeheron.Wallet, error) {
	var lastErr error
	for attempt := 0; attempt <= len(m.retryDelays); attempt++ {
		wallet, err := m.client.CreateAssetWallet(ctx, customerRefID, coinKeys)
		if err == nil {
			return wallet, nil
		}
		lastErr = err
		if attempt < len(m.retryDelays) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(m.retryDelays[attempt]):
			}
		}
	}
	return nil, fmt.Errorf("create wallet after %d retries: %w", len(m.retryDelays), lastErr)
}

