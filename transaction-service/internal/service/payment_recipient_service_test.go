package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// newRecipientTestDB creates a fresh in-memory SQLite database for each test,
// using the test name as a unique DSN to avoid shared state between tests.
func newRecipientTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s_recipient?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.PaymentRecipient{}))
	return db
}

func newRecipientService(t *testing.T) (*PaymentRecipientService, *repository.PaymentRecipientRepository) {
	t.Helper()
	db := newRecipientTestDB(t)
	repo := repository.NewPaymentRecipientRepository(db)
	svc := NewPaymentRecipientService(repo)
	return svc, repo
}

// TestPaymentRecipient_Create verifies that a new recipient is persisted with
// the correct fields and receives a non-zero auto-increment ID.
func TestPaymentRecipient_Create(t *testing.T) {
	svc, _ := newRecipientService(t)

	pr := &model.PaymentRecipient{
		ClientID:      42,
		RecipientName: "Marko Markovic",
		AccountNumber: "RS35000000000000000001",
	}
	require.NoError(t, svc.Create(pr))

	assert.NotZero(t, pr.ID, "ID must be set after Create")
	assert.Equal(t, uint64(42), pr.ClientID)
	assert.Equal(t, "Marko Markovic", pr.RecipientName)
	assert.Equal(t, "RS35000000000000000001", pr.AccountNumber)
}

// TestPaymentRecipient_ListByClient verifies that ListByClient returns only the
// recipients that belong to the requested client.
func TestPaymentRecipient_ListByClient(t *testing.T) {
	svc, _ := newRecipientService(t)

	// Seed two recipients for client 10, one for client 20.
	for _, pr := range []*model.PaymentRecipient{
		{ClientID: 10, RecipientName: "Alice", AccountNumber: "ACC-A"},
		{ClientID: 10, RecipientName: "Bob", AccountNumber: "ACC-B"},
		{ClientID: 20, RecipientName: "Carol", AccountNumber: "ACC-C"},
	} {
		require.NoError(t, svc.Create(pr))
	}

	list10, err := svc.ListByClient(10)
	require.NoError(t, err)
	assert.Len(t, list10, 2, "client 10 must have exactly 2 recipients")
	for _, r := range list10 {
		assert.Equal(t, uint64(10), r.ClientID, "all returned recipients must belong to client 10")
	}

	list20, err := svc.ListByClient(20)
	require.NoError(t, err)
	assert.Len(t, list20, 1, "client 20 must have exactly 1 recipient")
	assert.Equal(t, "Carol", list20[0].RecipientName)

	listUnknown, err := svc.ListByClient(999)
	require.NoError(t, err)
	assert.Empty(t, listUnknown, "unknown client ID must yield an empty list")
}

// TestPaymentRecipient_Update verifies that Update changes only the provided fields.
func TestPaymentRecipient_Update(t *testing.T) {
	svc, _ := newRecipientService(t)

	pr := &model.PaymentRecipient{
		ClientID:      5,
		RecipientName: "Original Name",
		AccountNumber: "OLD-ACC-001",
	}
	require.NoError(t, svc.Create(pr))
	id := pr.ID

	newName := "Updated Name"
	newAcc := "NEW-ACC-002"

	updated, err := svc.Update(id, &newName, &newAcc)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", updated.RecipientName)
	assert.Equal(t, "NEW-ACC-002", updated.AccountNumber)
	assert.Equal(t, id, updated.ID, "ID must not change after update")

	// Partial update: change only name, leave account number untouched.
	anotherName := "Partial Update"
	partial, err := svc.Update(id, &anotherName, nil)
	require.NoError(t, err)
	assert.Equal(t, "Partial Update", partial.RecipientName)
	assert.Equal(t, "NEW-ACC-002", partial.AccountNumber, "account number must not change on nil update")
}

// TestPaymentRecipient_Delete verifies that a deleted recipient can no longer be
// retrieved and is not returned in ListByClient.
func TestPaymentRecipient_Delete(t *testing.T) {
	svc, repo := newRecipientService(t)

	pr := &model.PaymentRecipient{
		ClientID:      7,
		RecipientName: "To Be Deleted",
		AccountNumber: "DEL-ACC-001",
	}
	require.NoError(t, svc.Create(pr))
	id := pr.ID

	require.NoError(t, svc.Delete(id))

	// GetByID must now return not-found.
	_, err := repo.GetByID(id)
	require.Error(t, err, "deleted recipient must not be retrievable")

	// ListByClient must return an empty list.
	list, err := svc.ListByClient(7)
	require.NoError(t, err)
	assert.Empty(t, list, "deleted recipient must not appear in ListByClient")
}
