package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCExpiryCron expires OTC contracts (releases the seller's reservation,
// seller keeps the premium) past their settlement_date. Covers both intra-bank
// (local option_contracts) and cross-bank (remote option_contracts rows, folded
// in by SP-2a) flows.
//
// OTC offers are termless "inventory" and are NOT expired here — they end only
// via cancel or accept. The former offer-expiry pass was removed.
type OTCExpiryCron struct {
	contracts     *repository.OptionContractRepository
	peerContracts *repository.OptionContractRepository // optional; nil disables remote-contract expiry
	holdingRes    *HoldingReservationService
	producer      *kafkaprod.Producer
	batchSize     int
	cronUTC       string

	// notifier emits in-app (push) notifications for OTC expiry events. Set
	// to the same *kafkaprod.Producer as `producer` by NewOTCExpiryCron;
	// tests inject a recording stub. nil disables in-app notifications.
	notifier otcNotifier

	// Outbox: optional, enables durable post-commit Kafka publish for the
	// expire events. When nil, the legacy direct-publish path is used so
	// unit tests that don't wire a DB still work.
	outbox   *outbox.Outbox
	outboxDB *gorm.DB

	// capitalGains, when wired, books the buyer's lost-premium capital loss
	// row at contract expiry (resolution-month model). nil disables (legacy
	// tests). Spec §4 C3.
	capitalGains CapitalGainRepo

	// warnDays > 0 enables the expiring-soon warning pass (SP5 E): contracts
	// whose settlement_date is exactly warnDays out get an
	// OTC_CONTRACT_EXPIRING_SOON in-app notification to both client parties.
	warnDays int

	entry *cronreg.Entry
}

// WithExpiryWarning enables the expiring-soon warning pass N days before
// settlement (SP5 E). 0 disables. Returns the cron for chaining.
func (cr *OTCExpiryCron) WithExpiryWarning(nDays int) *OTCExpiryCron {
	if nDays < 0 {
		nDays = 0
	}
	cr.warnDays = nDays
	return cr
}

// WithCapitalGains wires the capital-gain repo so contract expiry books the
// buyer's lost-premium loss row (TotalGain = -premium) in the expiry month.
// The seller keeps the premium (already taxed at accept). Spec §4 C3.
func (cr *OTCExpiryCron) WithCapitalGains(repo CapitalGainRepo) *OTCExpiryCron {
	cr.capitalGains = repo
	return cr
}

// WithOutbox wires the transactional outbox + the GORM handle the cron
// uses to enqueue rows. Callers that don't wire this fall back to the
// legacy direct-publish path (best-effort, may drop on crash).
func (cr *OTCExpiryCron) WithOutbox(ob *outbox.Outbox, db *gorm.DB) *OTCExpiryCron {
	cr.outbox = ob
	cr.outboxDB = db
	return cr
}

// WithPeerContracts wires the option-contract repo so the daily expiry pass
// also processes REMOTE option_contracts rows past their settlement_date.
// Optional — when nil, only intra-bank (local) contracts expire (legacy
// behaviour).
func (cr *OTCExpiryCron) WithPeerContracts(p *repository.OptionContractRepository) *OTCExpiryCron {
	cr.peerContracts = p
	return cr
}

func NewOTCExpiryCron(
	c *repository.OptionContractRepository,
	h *HoldingReservationService,
	p *kafkaprod.Producer,
	batchSize int, cronUTC string,
	registry *cronreg.Registry,
) *OTCExpiryCron {
	if batchSize <= 0 {
		batchSize = 500
	}
	if cronUTC == "" {
		cronUTC = "02:00"
	}
	cr := &OTCExpiryCron{contracts: c, holdingRes: h, producer: p, batchSize: batchSize, cronUTC: cronUTC}
	// Wire the notifier to the same producer. Guard against assigning a typed
	// nil into the interface (which would make cr.notifier != nil but panic on
	// call) by only setting it when the producer is actually present.
	if p != nil {
		cr.notifier = p
	}
	cr.entry = registry.Register("otc-expiry-cron", "Expire OTC contracts past their settlement date (daily)", 0)
	return cr
}

// notifyOTCPartyVia emits an in-app notification to one OTC party via the
// given notifier. No-op for bank parties / nil notifier; best-effort.
func notifyOTCPartyVia(ctx context.Context, n otcNotifier, party kafkamsg.OTCParty, notifType, refType string, refID uint64, data map[string]string) {
	if n == nil || party.OwnerType != "client" || party.OwnerID == nil {
		return
	}
	_ = n.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  *party.OwnerID,
		Type:    notifType,
		Data:    data,
		RefType: refType,
		RefID:   refID,
	})
}

// RunOnce executes the contract-expiry pass (local + remote) plus the
// expiring-soon warning pass. OTC offers are termless and never expire here.
func (cr *OTCExpiryCron) RunOnce(ctx context.Context) error {
	today := time.Now().UTC().Format("2006-01-02")
	for {
		rows, err := cr.contracts.ListExpiring(today, cr.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			if err := cr.expireContract(ctx, &rows[i]); err != nil {
				log.Printf("WARN: expire contract %d: %v", rows[i].ID, err)
			}
		}
	}
	// SP5 E: warn parties whose contract settles exactly warnDays from now.
	// Matching a single calendar day means each contract is warned once.
	if cr.warnDays > 0 {
		warnDay := time.Now().UTC().AddDate(0, 0, cr.warnDays)
		rows, err := cr.contracts.ListExpiringOn(warnDay, cr.batchSize)
		if err != nil {
			log.Printf("WARN: OTC expiring-soon list: %v", err)
		} else {
			for i := range rows {
				cr.warnContractExpiring(ctx, &rows[i])
			}
		}
	}
	if cr.peerContracts != nil {
		for {
			rows, err := cr.peerContracts.ListRemoteContractsExpiring(today, cr.batchSize)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for i := range rows {
				if err := cr.expirePeerContract(ctx, &rows[i]); err != nil {
					log.Printf("WARN: expire peer contract %d: %v", rows[i].ID, err)
				}
			}
		}
	}
	return nil
}

// expirePeerContract releases the seller's underlying-share lock (only
// meaningful on the seller's bank, where the remote row has direction=DEBIT;
// the buyer's bank held no lock to release) and transitions the contract to
// status="expired". Idempotent: re-running the cron over already-expired rows is
// a no-op because ListRemoteContractsExpiring filters on status="active".
func (cr *OTCExpiryCron) expirePeerContract(ctx context.Context, c *model.OptionContract) error {
	direction := ""
	if c.RemoteDirection != nil {
		direction = *c.RemoteDirection
	}
	if direction == "DEBIT" && cr.holdingRes != nil {
		if _, err := cr.holdingRes.ReleaseForPeerOptionContract(ctx, c.ID); err != nil {
			return err
		}
	}
	return cr.peerContracts.SetRemoteContractStatus(c.ID, "expired")
}

// warnContractExpiring sends an OTC_CONTRACT_EXPIRING_SOON in-app notification
// to both client parties of a contract approaching settlement (SP5 E). Bank
// parties / nil notifier are no-ops.
func (cr *OTCExpiryCron) warnContractExpiring(ctx context.Context, c *model.OptionContract) {
	data := map[string]string{
		"ticker":          c.Ticker,
		"settlement_date": c.SettlementDate.UTC().Format("2006-01-02"),
		"days_remaining":  fmt.Sprintf("%d", cr.warnDays),
	}
	notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID}, "OTC_CONTRACT_EXPIRING_SOON", "otc_contract", c.ID, data)
	notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID}, "OTC_CONTRACT_EXPIRING_SOON", "otc_contract", c.ID, data)
}

func (cr *OTCExpiryCron) expireContract(ctx context.Context, c *model.OptionContract) error {
	if cr.holdingRes != nil {
		if _, err := cr.holdingRes.ReleaseForOTCContract(ctx, c.ID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	// Resolution-month model: the buyer's premium is realised as a loss at
	// expiry, reducing their capital gain for the expiry month. The seller
	// keeps the premium (already taxed at accept). Booked BEFORE the status
	// flip so a crash between insert and flip re-runs safely; Create is
	// idempotent on the contract-scoped key (ON CONFLICT DO NOTHING), so a
	// re-run never double-books. Spec §3.1, §4 C3.
	if cr.capitalGains != nil && c.PremiumPaid.IsPositive() {
		lossKey := fmt.Sprintf("expire-contract-%d-buyer-premium-loss", c.ID)
		loss := &model.CapitalGain{
			OwnerType:        c.BuyerOwnerType,
			OwnerID:          c.BuyerOwnerID,
			OTC:              true,
			SecurityType:     "option",
			Ticker:           c.Ticker,
			Quantity:         c.Quantity.IntPart(),
			BuyPricePerUnit:  decimal.Zero,
			SellPricePerUnit: decimal.Zero,
			TotalGain:        c.PremiumPaid.Neg(),
			Currency:         c.PremiumCurrency,
			AccountID:        c.BuyerAccountID,
			TaxYear:          now.Year(),
			TaxMonth:         int(now.Month()),
			IdempotencyKey:   &lossKey,
		}
		if err := cr.capitalGains.Create(loss); err != nil {
			return err // do not flip status if the loss row failed; retry next pass
		}
	}
	c.Status = model.OptionContractStatusExpired
	c.ExpiredAt = &now
	if err := cr.contracts.Save(c); err != nil {
		return err
	}
	if cr.producer != nil {
		payload := kafkamsg.OTCContractExpiredMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: now.Format(time.RFC3339),
			ContractID: c.ID,
			Buyer: kafkamsg.OTCParty{
				OwnerType: string(c.BuyerOwnerType),
				OwnerID:   c.BuyerOwnerID,
			},
			Seller: kafkamsg.OTCParty{
				OwnerType: string(c.SellerOwnerType),
				OwnerID:   c.SellerOwnerID,
			},
			ExpiredAt: now.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			publishSagaEvent(ctx, cr.outbox, cr.outboxDB, cr.producer, kafkamsg.TopicOTCContractExpired, data, "")
		}
	}
	// In-app notifications to both client parties (no-op for bank parties /
	// nil notifier).
	ceData := map[string]string{"ticker": c.Ticker}
	notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{OwnerType: string(c.BuyerOwnerType), OwnerID: c.BuyerOwnerID}, "OTC_CONTRACT_EXPIRED", "otc_contract", c.ID, ceData)
	notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{OwnerType: string(c.SellerOwnerType), OwnerID: c.SellerOwnerID}, "OTC_CONTRACT_EXPIRED", "otc_contract", c.ID, ceData)
	return nil
}

// Start launches a goroutine that triggers RunOnce immediately (to
// catch up on missed expiries from any downtime that crossed a
// settlement date) and then daily at cronUTC. Honors context
// cancellation per CLAUDE.md.
func (cr *OTCExpiryCron) Start(ctx context.Context) {
	go func() {
		// Catch-up pass on startup. Best-effort — failures here are
		// logged and the daily schedule continues.
		if cr.entry.BeginRun() {
			err := cr.RunOnce(ctx)
			cr.entry.EndRun(err)
			if err != nil {
				log.Printf("WARN: OTC expiry startup run: %v", err)
			}
		}
		for {
			next := otcNextRunAt(time.Now().UTC(), cr.cronUTC)
			select {
			case <-time.After(time.Until(next)):
				if !cr.entry.BeginRun() {
					log.Println("OTC expiry cron: paused, skipping this tick")
					continue
				}
				err := cr.RunOnce(ctx)
				cr.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: OTC expiry run: %v", err)
				}
			case <-cr.entry.TriggerChan():
				if !cr.entry.BeginRun() {
					continue
				}
				err := cr.RunOnce(ctx)
				cr.entry.EndRun(err)
				if err != nil {
					log.Printf("WARN: OTC expiry triggered run: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func otcNextRunAt(now time.Time, hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		// Default to 02:00 UTC on any parse error.
		t, _ = time.Parse("15:04", "02:00")
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}
