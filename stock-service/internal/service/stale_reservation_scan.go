// Package service — StaleReservationScanner is the daily safety-net
// sweep added in Fix R8 (2026-05-16). It scans holding_reservations
// for rows that are still `active` past a long threshold (the saga
// that owns them has had ample time to settle or release). For each
// such row it CHECKS the linked entity's terminal status and LOGS at
// WARN — it does NOT auto-release. Auto-release is risky because a
// genuinely-in-flight long-running saga would have its lock yanked
// out from under it; the detector exists so operators can investigate
// + manually unwind cleanly.
//
// Account-side reservations (account_reservations) aren't covered by
// this scanner — account-service owns its own data and exposes no
// "linked entity terminal" predicate stock-service can query. A
// separate scanner in account-service would need its own design.
package service

import (
	"context"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// StaleReservationScanner walks the holding_reservations table once per
// tick and emits a WARN line for every active reservation whose linked
// entity (Order / OptionContract / PeerOptionContract) is in a terminal
// status. Wire it via Run; honors ctx cancellation.
type StaleReservationScanner struct {
	db            *gorm.DB
	resRepo       *repository.HoldingReservationRepository
	orderRepo     *repository.OrderRepository
	contractsRepo *repository.OptionContractRepository
	peerContracts *repository.PeerOptionContractRepository // optional

	interval  time.Duration
	threshold time.Duration
}

// NewStaleReservationScanner constructs the scanner. interval=24h and
// threshold=24h are reasonable defaults — anything held active longer
// than the threshold deserves a look (most sagas complete in seconds).
func NewStaleReservationScanner(
	db *gorm.DB,
	res *repository.HoldingReservationRepository,
	orders *repository.OrderRepository,
	contracts *repository.OptionContractRepository,
	interval, threshold time.Duration,
) *StaleReservationScanner {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if threshold <= 0 {
		threshold = 24 * time.Hour
	}
	return &StaleReservationScanner{
		db:            db,
		resRepo:       res,
		orderRepo:     orders,
		contractsRepo: contracts,
		interval:      interval,
		threshold:     threshold,
	}
}

// WithPeerContracts wires the cross-bank option-contract repo so the
// scanner can also classify peer-OTC reservations. Optional — when
// nil, only intra-bank order / OTC reservations are classified.
func (s *StaleReservationScanner) WithPeerContracts(p *repository.PeerOptionContractRepository) *StaleReservationScanner {
	s.peerContracts = p
	return s
}

// Run blocks until ctx is cancelled. First scan fires immediately so
// ops see baseline state; subsequent scans tick on s.interval.
func (s *StaleReservationScanner) Run(ctx context.Context) {
	s.scanOnce(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.scanOnce(ctx)
		}
	}
}

func (s *StaleReservationScanner) scanOnce(ctx context.Context) {
	cutoff := time.Now().Add(-s.threshold)
	var rows []model.HoldingReservation
	if err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", model.HoldingReservationStatusActive, cutoff).
		Find(&rows).Error; err != nil {
		log.Printf("WARN: stale-reservation scan: list active rows: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	stale := 0
	for i := range rows {
		r := &rows[i]
		switch {
		case r.OrderID != nil && *r.OrderID != 0:
			s.classifyOrderBacked(r, &stale)
		case r.OTCContractID != nil && *r.OTCContractID != 0:
			s.classifyOTCBacked(r, &stale)
		case r.PeerOptionContractID != nil && *r.PeerOptionContractID != 0 && s.peerContracts != nil:
			s.classifyPeerOTCBacked(r, &stale)
		}
	}
	log.Printf("INFO: stale-reservation scan complete: %d active rows older than %v, %d look stuck (linked entity terminal/missing)",
		len(rows), s.threshold, stale)
}

func (s *StaleReservationScanner) classifyOrderBacked(r *model.HoldingReservation, stale *int) {
	o, err := s.orderRepo.GetByID(*r.OrderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("WARN: stale holding_reservation id=%d order_id=%d: order MISSING (orphan) — needs manual release",
				r.ID, *r.OrderID)
			*stale++
		}
		return
	}
	switch o.Status {
	case "filled", "cancelled", "expired", "rejected", "failed":
		log.Printf("WARN: stale holding_reservation id=%d order_id=%d: order.status=%s — needs manual release",
			r.ID, *r.OrderID, o.Status)
		*stale++
	}
}

func (s *StaleReservationScanner) classifyOTCBacked(r *model.HoldingReservation, stale *int) {
	c, err := s.contractsRepo.GetByID(*r.OTCContractID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("WARN: stale holding_reservation id=%d otc_contract_id=%d: contract MISSING (orphan) — needs manual release",
				r.ID, *r.OTCContractID)
			*stale++
		}
		return
	}
	switch c.Status {
	case model.OptionContractStatusExercised,
		model.OptionContractStatusExpired,
		model.OptionContractStatusFailed:
		log.Printf("WARN: stale holding_reservation id=%d otc_contract_id=%d: contract.status=%s — needs manual release",
			r.ID, *r.OTCContractID, c.Status)
		*stale++
	}
}

func (s *StaleReservationScanner) classifyPeerOTCBacked(r *model.HoldingReservation, stale *int) {
	c, err := s.peerContracts.GetByID(*r.PeerOptionContractID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("WARN: stale holding_reservation id=%d peer_option_contract_id=%d: peer contract MISSING — needs manual release",
				r.ID, *r.PeerOptionContractID)
			*stale++
		}
		return
	}
	if c.Status == "exercised" || c.Status == "expired" {
		log.Printf("WARN: stale holding_reservation id=%d peer_option_contract_id=%d: peer contract.status=%s — needs manual release",
			r.ID, *r.PeerOptionContractID, c.Status)
		*stale++
	}
}
