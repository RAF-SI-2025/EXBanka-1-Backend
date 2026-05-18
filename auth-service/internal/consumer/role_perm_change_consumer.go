package consumer

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	kafkamsg "github.com/exbanka/contract/kafka"
)

// RevokeCache is the narrow Redis surface the handler needs.
type RevokeCache interface {
	SetUserRevokedAt(ctx context.Context, userID int64, atUnix int64, ttl time.Duration) error
}

// RefreshTokenRevoker revokes all refresh tokens for a given principal so the
// affected employee MUST go through full re-login (not just refresh) on next
// API call.
type RefreshTokenRevoker interface {
	RevokeAllForPrincipal(principalType string, principalID int64) error
}

// RolePermChangeHandler is split from the Kafka reader for direct unit testing.
type RolePermChangeHandler struct {
	cache    RevokeCache
	tokens   RefreshTokenRevoker
	epochTTL time.Duration
}

func NewRolePermChangeHandler(cache RevokeCache, tokens RefreshTokenRevoker, epochTTL time.Duration) *RolePermChangeHandler {
	return &RolePermChangeHandler{cache: cache, tokens: tokens, epochTTL: epochTTL}
}

func (h *RolePermChangeHandler) Handle(ctx context.Context, raw []byte) error {
	var msg kafkamsg.RolePermissionsChangedMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	for _, empID := range msg.AffectedEmployeeIDs {
		if err := h.cache.SetUserRevokedAt(ctx, empID, msg.ChangedAt, h.epochTTL); err != nil {
			log.Printf("WARN: role-perm-change: set epoch for employee %d: %v", empID, err)
		}
		if err := h.tokens.RevokeAllForPrincipal("employee", empID); err != nil {
			log.Printf("WARN: role-perm-change: revoke refresh tokens for employee %d: %v", empID, err)
		}
	}
	return nil
}

// RolePermChangeConsumer is the long-running Kafka consumer wrapper.
type RolePermChangeConsumer struct {
	reader  *kafka.Reader
	handler *RolePermChangeHandler
}

func NewRolePermChangeConsumer(brokers string, h *RolePermChangeHandler) *RolePermChangeConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{brokers},
		Topic:       kafkamsg.TopicUserRolePermissionsChanged,
		GroupID:     "auth-service-role-perm-change",
		StartOffset: kafka.LastOffset,
	})
	return &RolePermChangeConsumer{reader: r, handler: h}
}

func (c *RolePermChangeConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("role-perm-change consumer read error: %v", err)
				continue
			}
			if err := c.handler.Handle(ctx, msg.Value); err != nil {
				log.Printf("role-perm-change consumer handle error: %v (payload: %s)", err, string(msg.Value))
			}
		}
	}()
}

func (c *RolePermChangeConsumer) Close() error {
	return c.reader.Close()
}
