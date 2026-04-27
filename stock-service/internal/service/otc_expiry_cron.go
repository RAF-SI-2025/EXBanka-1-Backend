package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCExpiryCron expires OTC contracts (releases the seller's reservation,
// seller keeps the premium) and OTC offers (no money flow) past their
// settlement_date.
type OTCExpiryCron struct {
	contracts  *repository.OptionContractRepository
	offers     *repository.OTCOfferRepository
	holdingRes *HoldingReservationService
	producer   *kafkaprod.Producer
	batchSize  int
	cronUTC    string
}

func NewOTCExpiryCron(
	c *repository.OptionContractRepository,
	o *repository.OTCOfferRepository,
	h *HoldingReservationService,
	p *kafkaprod.Producer,
	batchSize int, cronUTC string,
) *OTCExpiryCron {
	if batchSize <= 0 {
		batchSize = 500
	}
	if cronUTC == "" {
		cronUTC = "02:00"
	}
	return &OTCExpiryCron{contracts: c, offers: o, holdingRes: h, producer: p, batchSize: batchSize, cronUTC: cronUTC}
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
	return nil
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
		// Kafka payload still uses the legacy OTCParty(user_id, system_type)
		// shape pending Task 9 of plan 2026-04-27-owner-type-schema.md.
		payload := kafkamsg.OTCContractExpiredMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: now.Format(time.RFC3339),
			ContractID: c.ID,
			Buyer: kafkamsg.OTCParty{
				UserID:     int64(model.OwnerToLegacyUserID(c.BuyerOwnerType, c.BuyerOwnerID)),
				SystemType: model.OwnerToLegacySystemType(c.BuyerOwnerType),
			},
			Seller: kafkamsg.OTCParty{
				UserID:     int64(model.OwnerToLegacyUserID(c.SellerOwnerType, c.SellerOwnerID)),
				SystemType: model.OwnerToLegacySystemType(c.SellerOwnerType),
			},
			ExpiredAt: now.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = cr.producer.PublishRaw(ctx, kafkamsg.TopicOTCContractExpired, data)
		}
	}
	return nil
}

func (cr *OTCExpiryCron) expireOffer(ctx context.Context, o *model.OTCOffer) error {
	o.Status = model.OTCOfferStatusExpired
	if err := cr.offers.Save(o); err != nil {
		return err
	}
	if cr.producer != nil {
		// Kafka payload still uses the legacy OTCParty(user_id, system_type)
		// shape pending Task 9 of plan 2026-04-27-owner-type-schema.md.
		payload := kafkamsg.OTCOfferExpiredMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID:    o.ID,
			Initiator: kafkamsg.OTCParty{
				UserID:     int64(model.OwnerToLegacyUserID(o.InitiatorOwnerType, o.InitiatorOwnerID)),
				SystemType: model.OwnerToLegacySystemType(o.InitiatorOwnerType),
			},
			Counterparty: ptrCounterparty(o),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = cr.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferExpired, data)
		}
	}
	return nil
}

// Start launches a goroutine that triggers RunOnce daily at cronUTC. Honors
// context cancellation per CLAUDE.md.
func (cr *OTCExpiryCron) Start(ctx context.Context) {
	go func() {
		for {
			next := otcNextRunAt(time.Now().UTC(), cr.cronUTC)
			select {
			case <-time.After(time.Until(next)):
				if err := cr.RunOnce(ctx); err != nil {
					log.Printf("WARN: OTC expiry run: %v", err)
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
