package model

import "time"

// ClientReplica is a local read-model of a client's profile, fed by
// client.created / client.updated Kafka events (SP-1). It is NOT authoritative —
// client-service owns the client. Used to avoid synchronous GetClient hot-path reads.
type ClientReplica struct {
	ID        uint64 `gorm:"primaryKey"` // == client-service Client.ID (no autoincrement)
	Email     string `gorm:"not null"`
	FirstName string `gorm:"not null"`
	LastName  string `gorm:"not null"`
	JMBG      string `gorm:"size:13"`
	Version   int64  `gorm:"not null;default:0"` // source Client.Version; ordering guard
	UpdatedAt time.Time
}
