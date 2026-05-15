package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// recordingMaintenanceProducer captures published events so the helper's
// emit behavior can be asserted without a real Kafka broker.
type recordingMaintenanceProducer struct {
	domainCalls []kafkamsg.MaintenanceFeeChargedMessage
	notifs      []kafkamsg.GeneralNotificationMessage
}

func (r *recordingMaintenanceProducer) PublishMaintenanceFeeCharged(_ context.Context, m kafkamsg.MaintenanceFeeChargedMessage) error {
	r.domainCalls = append(r.domainCalls, m)
	return nil
}

func (r *recordingMaintenanceProducer) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}

func TestMaintenanceCron_ClientAccount_EmitsDomainAndNotification(t *testing.T) {
	rec := &recordingMaintenanceProducer{}
	svc := &MaintenanceCronService{producer: rec}

	acc := &model.Account{
		ID:             42,
		AccountNumber:  "1110000000000042",
		OwnerID:        7,
		CurrencyCode:   "RSD",
		MaintenanceFee: decimal.NewFromFloat(199.99),
		IsBankAccount:  false,
	}

	svc.notifyMaintenanceFeeCharged(context.Background(), acc)

	require.Len(t, rec.domainCalls, 1, "expected one domain MaintenanceFeeCharged event")
	domain := rec.domainCalls[0]
	assert.Equal(t, "1110000000000042", domain.AccountNumber)
	assert.Equal(t, "199.99", domain.Amount)
	assert.Equal(t, "RSD", domain.CurrencyCode)

	require.Len(t, rec.notifs, 1, "expected one in-app notification for client account")
	n := rec.notifs[0]
	assert.Equal(t, "MAINTENANCE_FEE_CHARGED", n.Type)
	assert.Equal(t, uint64(7), n.UserID)
	assert.Equal(t, "account", n.RefType)
	assert.Equal(t, uint64(42), n.RefID)
	require.NotNil(t, n.Data)
	assert.Equal(t, "1110000000000042", n.Data["account_number"])
	assert.Equal(t, "199.99", n.Data["amount"])
	assert.Equal(t, "RSD", n.Data["currency"])
}

func TestMaintenanceCron_BankAccount_DomainOnlyNoNotification(t *testing.T) {
	rec := &recordingMaintenanceProducer{}
	svc := &MaintenanceCronService{producer: rec}

	acc := &model.Account{
		ID:             1,
		AccountNumber:  "1110000000000001",
		OwnerID:        1_000_000_000, // bank sentinel
		CurrencyCode:   "RSD",
		MaintenanceFee: decimal.NewFromFloat(50.00),
		IsBankAccount:  true,
	}

	svc.notifyMaintenanceFeeCharged(context.Background(), acc)

	require.Len(t, rec.domainCalls, 1, "domain event must still fire for bank accounts")
	assert.Equal(t, "50.00", rec.domainCalls[0].Amount)
	assert.Empty(t, rec.notifs, "bank accounts must not emit user-facing notifications")
}

func TestMaintenanceCron_NilProducer_NoPanic(t *testing.T) {
	svc := &MaintenanceCronService{producer: nil}
	acc := &model.Account{
		ID:             10,
		AccountNumber:  "1110000000000010",
		OwnerID:        5,
		CurrencyCode:   "EUR",
		MaintenanceFee: decimal.NewFromFloat(2.50),
		IsBankAccount:  false,
	}

	assert.NotPanics(t, func() {
		svc.notifyMaintenanceFeeCharged(context.Background(), acc)
	})
}
