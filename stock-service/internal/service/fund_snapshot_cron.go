package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// fundSnapshotSource is the narrow slice of FundService the snapshot cron needs:
// list active funds and compute their current statistics (NAV + components).
type fundSnapshotSource interface {
	List(search string, active *bool, page, pageSize int) ([]model.InvestmentFund, int64, error)
	Statistics(ctx context.Context, fund *model.InvestmentFund) (FundStatistics, error)
}

// FundSnapshotCron writes one daily FundValueSnapshot per active fund so the
// discovery/detail metrics and the historical value chart have a NAV series.
// Mirrors ListingCronService.
type FundSnapshotCron struct {
	funds     fundSnapshotSource
	snapshots *repository.FundValueSnapshotRepository
	cronUTC   string
	registry  *cronreg.Registry
	entry     *cronreg.Entry
}

func NewFundSnapshotCron(funds fundSnapshotSource, snapshots *repository.FundValueSnapshotRepository, cronUTC string, registry *cronreg.Registry) *FundSnapshotCron {
	if cronUTC == "" {
		cronUTC = "23:50"
	}
	cr := &FundSnapshotCron{funds: funds, snapshots: snapshots, cronUTC: cronUTC, registry: registry}
	cr.entry = registry.Register("fund-snapshot-cron", "Snapshot each active fund's NAV daily for statistics/history", 0)
	return cr
}

// RunOnce snapshots every active fund's current NAV dated today. Idempotent:
// re-running within the same day overwrites today's row. Per-fund failures are
// logged and skipped so one bad fund does not abort the batch.
func (cr *FundSnapshotCron) RunOnce(ctx context.Context) error {
	active := true
	funds, _, err := cr.funds.List("", &active, 1, 100000)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	count := 0
	for i := range funds {
		f := &funds[i]
		stat, sErr := cr.funds.Statistics(ctx, f)
		if sErr != nil {
			log.Printf("WARN: fund snapshot cron: stats for fund %d failed: %v", f.ID, sErr)
			continue
		}
		snap := &model.FundValueSnapshot{
			FundID:           f.ID,
			Date:             today,
			TotalValueRSD:    stat.TotalValueRSD,
			LiquidRSDBal:     stat.LiquidRSDBal,
			HoldingsValueRSD: stat.TotalHoldingsValueRSD,
			InvestorCount:    stat.InvestorCount,
		}
		if uErr := cr.snapshots.UpsertByFundAndDate(snap); uErr != nil {
			log.Printf("WARN: fund snapshot cron: upsert fund %d failed: %v", f.ID, uErr)
			continue
		}
		count++
	}
	log.Printf("fund snapshot cron: snapshotted %d funds for %s", count, today.Format("2006-01-02"))
	return nil
}

// StartDailyCron runs a startup catch-up pass, then daily at cronUTC. Honors
// cronreg pause (BeginRun/EndRun), manual triggers, and ctx cancellation.
func (cr *FundSnapshotCron) StartDailyCron(ctx context.Context) {
	go func() {
		if cr.entry.BeginRun() {
			err := cr.RunOnce(ctx)
			cr.entry.EndRun(err)
			if err != nil {
				log.Printf("WARN: fund snapshot startup run: %v", err)
			}
		}
		for {
			next := fundSnapshotNextRunAt(time.Now().UTC(), cr.cronUTC)
			select {
			case <-time.After(time.Until(next)):
				if !cr.entry.BeginRun() {
					log.Println("fund snapshot cron: paused, skipping this tick")
					continue
				}
				err := cr.RunOnce(ctx)
				cr.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: fund snapshot run: %v", err)
				}
			case <-cr.entry.TriggerChan():
				if !cr.entry.BeginRun() {
					continue
				}
				err := cr.RunOnce(ctx)
				cr.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: fund snapshot triggered run: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Printf("fund snapshot cron: scheduled daily at %s UTC", cr.cronUTC)
}

func fundSnapshotNextRunAt(now time.Time, hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		t, _ = time.Parse("15:04", "23:50")
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}
