package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// TestPaymentRecipientRepository_DeleteIfExists verifies the richer delete
// signal: a present row reports ok=true, while a missing id reports ok=false
// (and no error) so the service layer can surface 404.
func TestPaymentRecipientRepository_DeleteIfExists(t *testing.T) {
	db := newCommonDB(t)
	r := repository.NewPaymentRecipientRepository(db)

	pr := &model.PaymentRecipient{ClientID: 1, RecipientName: "alice", AccountNumber: "111-1"}
	require.NoError(t, r.Create(pr))

	// Existing row → removed → ok true.
	ok, err := r.DeleteIfExists(pr.ID)
	require.NoError(t, err)
	assert.True(t, ok, "deleting an existing recipient must report ok=true")

	// Second delete of the same id → no row → ok false, no error.
	ok, err = r.DeleteIfExists(pr.ID)
	require.NoError(t, err)
	assert.False(t, ok, "deleting a missing recipient must report ok=false")

	// Never-existed id → ok false.
	ok, err = r.DeleteIfExists(999999)
	require.NoError(t, err)
	assert.False(t, ok)
}
