package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/credit-service/internal/model"
)

// nilRegistry returns a no-op Registry suitable for unit tests that don't
// need pause/trigger control (nil PauseStore is explicitly allowed).
func nilRegistry() *cronreg.Registry {
	return cronreg.NewRegistry("test", nil)
}

// recordingCronNotifier captures every GeneralNotificationMessage published by
// CronService so tests can assert on Type, UserID, Data, RefType, and RefID.
type recordingCronNotifier struct {
	notifs []kafkamsg.GeneralNotificationMessage
}

func (r *recordingCronNotifier) PublishGeneralNotification(_ context.Context, m kafkamsg.GeneralNotificationMessage) error {
	r.notifs = append(r.notifs, m)
	return nil
}

// TestCronService_InstallmentCollected_EmitsNotification verifies that the
// notifyInstallment helper publishes an INSTALLMENT_COLLECTED in-app notification
// for the borrower with the installment amount and reference.
func TestCronService_InstallmentCollected_EmitsNotification(t *testing.T) {
	rec := &recordingCronNotifier{}
	cron := &CronService{notifier: rec}

	loan := &model.Loan{ClientID: 42}
	cron.notifyInstallment(context.Background(), loan, 7, "INSTALLMENT_COLLECTED", "1000.0000", "")

	require.Len(t, rec.notifs, 1, "expected exactly one notification emit")
	n := rec.notifs[0]
	assert.Equal(t, "INSTALLMENT_COLLECTED", n.Type)
	assert.Equal(t, uint64(42), n.UserID)
	assert.Equal(t, "installment", n.RefType)
	assert.Equal(t, uint64(7), n.RefID)
	assert.NotEmpty(t, n.Data["amount"], "amount data key must be set")
	// retry_deadline only relevant on the failure path
	assert.Empty(t, n.Data["retry_deadline"], "retry_deadline must not be set on success")
}

// TestCronService_InstallmentFailed_EmitsNotification verifies that the
// notifyInstallment helper publishes an INSTALLMENT_FAILED in-app notification
// with the amount and retry_deadline.
func TestCronService_InstallmentFailed_EmitsNotification(t *testing.T) {
	rec := &recordingCronNotifier{}
	cron := &CronService{notifier: rec}

	loan := &model.Loan{ClientID: 99}
	cron.notifyInstallment(context.Background(), loan, 13, "INSTALLMENT_FAILED", "2500.0000", "2026-05-18T00:00:00Z")

	require.Len(t, rec.notifs, 1, "expected exactly one notification emit")
	n := rec.notifs[0]
	assert.Equal(t, "INSTALLMENT_FAILED", n.Type)
	assert.Equal(t, uint64(99), n.UserID)
	assert.Equal(t, "installment", n.RefType)
	assert.Equal(t, uint64(13), n.RefID)
	assert.NotEmpty(t, n.Data["amount"], "amount data key must be set")
	assert.NotEmpty(t, n.Data["retry_deadline"], "retry_deadline data key must be set on failure")
}

// TestCronService_NotifyInstallment_NilNotifier_NoPanic verifies the helper is
// safe when notifier is nil (e.g. credit-service started without Kafka).
func TestCronService_NotifyInstallment_NilNotifier_NoPanic(t *testing.T) {
	cron := &CronService{}
	loan := &model.Loan{ClientID: 1}
	// Must not panic and must not emit anything.
	cron.notifyInstallment(context.Background(), loan, 1, "INSTALLMENT_COLLECTED", "100.0000", "")
}
