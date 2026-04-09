package changelog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiff_DetectsChanges(t *testing.T) {
	entries := Diff("account", 42, 1, "", []FieldChange{
		{Field: "daily_limit", OldValue: "100000", NewValue: "200000"},
		{Field: "monthly_limit", OldValue: "500000", NewValue: "500000"}, // no change
		{Field: "status", OldValue: "active", NewValue: "inactive"},
	})
	require.Len(t, entries, 2)
	assert.Equal(t, "daily_limit", entries[0].FieldName)
	assert.Equal(t, "status", entries[1].FieldName)
	assert.Equal(t, int64(42), entries[0].EntityID)
	assert.Equal(t, int64(1), entries[0].ChangedBy)
}

func TestDiff_NoChanges_ReturnsEmpty(t *testing.T) {
	entries := Diff("account", 1, 1, "", []FieldChange{
		{Field: "name", OldValue: "foo", NewValue: "foo"},
	})
	assert.Empty(t, entries)
}

func TestNewStatusChangeEntry(t *testing.T) {
	entry := NewStatusChangeEntry("card", 10, 5, "active", "blocked", "suspicious activity")
	assert.Equal(t, ActionStatusChange, entry.Action)
	assert.Equal(t, "status", entry.FieldName)
	assert.Equal(t, `"active"`, entry.OldValue)
	assert.Equal(t, `"blocked"`, entry.NewValue)
	assert.Equal(t, "suspicious activity", entry.Reason)
}

func TestNewCreateEntry(t *testing.T) {
	entry := NewCreateEntry("loan", 7, 3, `{"amount": 50000}`)
	assert.Equal(t, ActionCreate, entry.Action)
	assert.Empty(t, entry.FieldName)
	assert.Empty(t, entry.OldValue)
	assert.Equal(t, `{"amount": 50000}`, entry.NewValue)
}

func TestToJSON_String(t *testing.T) {
	assert.Equal(t, `"hello"`, ToJSON("hello"))
}

func TestToJSON_Nil(t *testing.T) {
	assert.Equal(t, "", ToJSON(nil))
}

func TestToJSON_Struct(t *testing.T) {
	v := map[string]int{"x": 1}
	assert.Equal(t, `{"x":1}`, ToJSON(v))
}
