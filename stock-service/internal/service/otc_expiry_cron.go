package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCExpiryCron expires OTC contracts (releases the seller's reservation,
// seller keeps the premium) and OTC offers (no money flow) past their
// settlement_date. Covers both intra-bank (option_contracts) and
// cross-bank (peer_option_contracts) flows.
type OTCExpiryCron struct {
	contracts     *repository.OptionContractRepository
	peerContracts *repository.PeerOptionContractRepository // optional; nil disables peer-contract expiry
	offers        *repository.OTCOfferRepository
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

	entry *cronreg.Entry
}

// WithOutbox wires the transactional outbox + the GORM handle the cron
// uses to enqueue rows. Callers that don't wire this fall back to the
// legacy direct-publish path (best-effort, may drop on crash).
func (cr *OTCExpiryCron) WithOutbox(ob *outbox.Outbox, db *gorm.DB) *OTCExpiryCron {
	cr.outbox = ob
	cr.outboxDB = db
	return cr
}

// WithPeerContracts wires the cross-bank option contracts repo so the
// daily expiry pass also processes peer_option_contracts rows past
// their settlement_date. Optional — when nil, only intra-bank contracts
// expire (legacy behaviour).
func (cr *OTCExpiryCron) WithPeerContracts(p *repository.PeerOptionContractRepository) *OTCExpiryCron {
	cr.peerContracts = p
	return cr
}

func NewOTCExpiryCron(
	c *repository.OptionContractRepository,
	o *repository.OTCOfferRepository,
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
	cr := &OTCExpiryCron{contracts: c, offers: o, holdingRes: h, producer: p, batchSize: batchSize, cronUTC: cronUTC}
	// Wire the notifier to the same producer. Guard against assigning a typed
	// nil into the interface (which would make cr.notifier != nil but panic on
	// call) by only setting it when the producer is actually present.
	if p != nil {
		cr.notifier = p
	}
	cr.entry = registry.Register("otc-expiry-cron", "Expire OTC contracts and offers past their settlement date (daily)", 0)
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

// RunOnce executes both expiry passes (contracts + offers).
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
	for {
		rows, err := cr.offers.ListExpiringOffers(today, cr.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			if err := cr.expireOffer(ctx, &rows[i]); err != nil {
				log.Printf("WARN: expire offer %d: %v", rows[i].ID, err)
			}
		}
	}
	if cr.peerContracts != nil {
		for {
			rows, err := cr.peerContracts.ListExpiring(today, cr.batchSize)
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
// meaningful on the seller's bank, where the row has direction=DEBIT;
// the buyer's bank held no lock to release) and transitions the
// contract to status="expired". Idempotent: re-running the cron over
// already-expired rows is a no-op because ListExpiring filters on
// status="active".
func (cr *OTCExpiryCron) expirePeerContract(ctx context.Context, c *model.PeerOptionContract) error {
	if c.Direction == "DEBIT" && cr.holdingRes != nil {
		if _, err := cr.holdingRes.ReleaseForPeerOptionContract(ctx, c.ID); err != nil {
			return err
		}
	}
	return cr.peerContracts.SetStatus(c.ID, "expired")
}

func (cr *OTCExpiryCron) expireContract(ctx context.Context, c *model.OptionContract) error {
	if cr.holdingRes != nil {
		if _, err := cr.holdingRes.ReleaseForOTCContract(ctx, c.ID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
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

func (cr *OTCExpiryCron) expireOffer(ctx context.Context, o *model.OTCOffer) error {
	o.Status = model.OTCOfferStatusExpired
	if err := cr.offers.Save(o); err != nil {
		return err
	}
	if cr.producer != nil {
		payload := kafkamsg.OTCOfferExpiredMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID:    o.ID,
			Initiator: kafkamsg.OTCParty{
				OwnerType: string(o.InitiatorOwnerType),
				OwnerID:   o.InitiatorOwnerID,
			},
			Counterparty: ptrCounterparty(o),
		}
		if data, err := json.Marshal(payload); err == nil {
			publishSagaEvent(ctx, cr.outbox, cr.outboxDB, cr.producer, kafkamsg.TopicOTCOfferExpired, data, "")
		}
	}
	// In-app notifications to the initiator + counterparty client parties
	// (no-op for bank parties / nil notifier).
	notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{
		OwnerType: string(o.InitiatorOwnerType), OwnerID: o.InitiatorOwnerID,
	}, "OTC_OFFER_EXPIRED", "otc_offer", o.ID, map[string]string{"ticker": o.Ticker})
	if o.CounterpartyOwnerType != nil {
		notifyOTCPartyVia(ctx, cr.notifier, kafkamsg.OTCParty{
			OwnerType: string(*o.CounterpartyOwnerType), OwnerID: o.CounterpartyOwnerID,
		}, "OTC_OFFER_EXPIRED", "otc_offer", o.ID, map[string]string{"ticker": o.Ticker})
	}
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
