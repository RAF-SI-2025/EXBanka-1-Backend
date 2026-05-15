package service

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
)

// FundLifecycleCron walks closed-end funds and transitions their
// FundStatus based on the calendar. Pure state-machine — no money
// movement yet (auto-liquidation is the follow-up wire-up). Transitions:
//
//   fundraising → active   when now > fundraising_end
//   active      → matured  when now > maturity_date (sets MaturityGraceEnd = +7d)
//   matured     → liquidated when now > maturity_grace_end (placeholder: real
//                            distribution requires Sell-Market + pro-rata payout)
//
// Each transition fires a general notification to the fund manager.
type FundLifecycleCron struct {
	db       *gorm.DB
	notifier recurringOrderNotifier
	interval time.Duration
}

func NewFundLifecycleCron(db *gorm.DB, notifier recurringOrderNotifier, interval time.Duration) *FundLifecycleCron {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	c := &FundLifecycleCron{db: db, interval: interval}
	if notifier != nil {
		c.notifier = notifier
	}
	return c
}

func (c *FundLifecycleCron) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(ctx, time.Now().UTC())
		}
	}
}

func (c *FundLifecycleCron) tick(ctx context.Context, now time.Time) {
	var funds []model.InvestmentFund
	if err := c.db.Where("fund_type = ?", model.FundTypeClosed).Find(&funds).Error; err != nil {
		log.Printf("WARN: fund-lifecycle list failed: %v", err)
		return
	}
	for i := range funds {
		f := &funds[i]
		if c.advance(ctx, f, now) {
			if err := c.db.Save(f).Error; err != nil {
				log.Printf("WARN: fund-lifecycle save failed for fund %d: %v", f.ID, err)
			}
		}
	}
}

// advance mutates the fund in place. Returns true when state changed.
func (c *FundLifecycleCron) advance(ctx context.Context, f *model.InvestmentFund, now time.Time) bool {
	switch f.FundStatus {
	case model.FundStatusFundraising:
		if f.FundraisingEnd != nil && now.After(*f.FundraisingEnd) {
			f.FundStatus = model.FundStatusActive
			c.notify(ctx, f, "FUND_FUNDRAISING_CLOSED")
			return true
		}
		// Allow late entry into fundraising: scheduled creation may pre-date
		// the start, in which case we transition open → fundraising at start.
		if f.FundraisingStart != nil && now.After(*f.FundraisingStart) {
			// already fundraising — no-op
		}
	case model.FundStatusOpen:
		// Closed funds shouldn't sit in "open"; clamp them to fundraising
		// at first tick after FundraisingStart.
		if f.FundraisingStart != nil && now.After(*f.FundraisingStart) {
			f.FundStatus = model.FundStatusFundraising
			c.notify(ctx, f, "FUND_FUNDRAISING_STARTED")
			return true
		}
	case model.FundStatusActive:
		if f.MaturityDate != nil && now.After(*f.MaturityDate) {
			f.FundStatus = model.FundStatusMatured
			grace := f.MaturityDate.Add(7 * 24 * time.Hour)
			f.MaturityGraceEnd = &grace
			c.notify(ctx, f, "FUND_MATURED")
			return true
		}
	case model.FundStatusMatured:
		if f.MaturityGraceEnd != nil && now.After(*f.MaturityGraceEnd) {
			// Auto-liquidation logic (sell remaining holdings + pro-rata
			// distribution) is deferred. For now we flip to liquidated
			// so the lifecycle terminates; the supervisor handles the
			// money movement manually until the wire-up lands.
			f.FundStatus = model.FundStatusLiquidated
			f.Active = false
			c.notify(ctx, f, "FUND_LIQUIDATED")
			return true
		}
	}
	return false
}

func (c *FundLifecycleCron) notify(ctx context.Context, f *model.InvestmentFund, key string) {
	if c.notifier == nil {
		return
	}
	uid := uint64(f.ManagerEmployeeID)
	_ = c.notifier.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID: uid,
		Type:   key,
		Data: map[string]string{
			"fund_id":   kafkaUint(f.ID),
			"fund_name": f.Name,
		},
		RefType: "fund",
		RefID:   f.ID,
	})
}
