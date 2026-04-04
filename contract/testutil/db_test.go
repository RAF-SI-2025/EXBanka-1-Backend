package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sampleModel struct {
	ID   uint64 `gorm:"primaryKey"`
	Name string
}

func TestSetupTestDB_MigratesAndAllowsCRUD(t *testing.T) {
	db := SetupTestDB(t, &sampleModel{})
	require.NotNil(t, db)

	err := db.Create(&sampleModel{ID: 1, Name: "test"}).Error
	require.NoError(t, err)

	var got sampleModel
	err = db.First(&got, 1).Error
	require.NoError(t, err)
	assert.Equal(t, "test", got.Name)
}

func TestSetupTestDB_IsolatedPerCall(t *testing.T) {
	db1 := SetupTestDB(t, &sampleModel{})
	db2 := SetupTestDB(t, &sampleModel{})

	_ = db1.Create(&sampleModel{ID: 1, Name: "db1"}).Error

	var got sampleModel
	err := db2.First(&got, 1).Error
	assert.Error(t, err, "db2 should be empty — databases should be isolated")
}
