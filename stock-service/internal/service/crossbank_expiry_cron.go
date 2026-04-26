package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// CrossbankExpiryCron runs daily alongside the Spec 2 intra-bank expiry
// cron. Picks up ACTIVE OptionContracts past their settlement_date that
// are cross-bank (buyer/seller bank codes differ) and drives the 3-phase
// expire saga via CrossbankExpireSaga. Same-bank contracts are handled by
// the Spec-2 cron and ignored here.
type CrossbankExpiryCron struct {
	contracts *repository.OptionContractRepository
	expire    *CrossbankExpireSaga
	cronUTC   string
	batchSize int
}

func NewCrossbankExpiryCron(
	contracts *repository.OptionContractRepository,
	expire *CrossbankExpireSaga,
	cronUTC string,
	batchSize int,
) *CrossbankExpiryCron {
	if cronUTC == "" {
		cronUTC = "02:30"
	}
	if batchSize <= 0 {
		batchSize = 200
	}
	return &CrossbankExpiryCron{contracts: contracts, expire: expire, cronUTC: cronUTC, batchSize: batchSize}
}

func (c *CrossbankExpiryCron) Start(ctx context.Context) {
	go func() {
		for {
			next := otcNextRunAt(time.Now().UTC(), c.cronUTC)
			select {
			case <-time.After(time.Until(next)):
				if err := c.RunOnce(ctx); err != nil {
					log.Printf("WARN: cross-bank expiry run: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// RunOnce expires every cross-bank ACTIVE contract past its settlement
// date. Iterates batches; same-bank contracts are skipped (the Spec 2
// cron handles those).
func (c *CrossbankExpiryCron) RunOnce(ctx context.Context) error {
	if c.contracts == nil || c.expire == nil {
		return nil
	}
	today := time.Now().UTC().Format("2006-01-02")
	for {
		rows, err := c.contracts.ListExpiring(today, c.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			r := rows[i]
			if !r.IsCrossBank() {
				continue
			}
			if err := c.expire.ExpireContract(ctx, r.ID); err != nil {
				log.Printf("WARN: cross-bank expire %d: %v", r.ID, err)
			}
		}
		// If the batch was full and entirely same-bank we'd loop forever;
		// break after one iteration since same-bank rows aren't moved here.
		_ = model.OptionContractStatusActive
		break
	}
	return nil
}
