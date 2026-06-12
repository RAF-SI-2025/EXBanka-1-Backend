package service

import (
	"context"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"

	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	clientpb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// emitTimeout bounds the email-lookup gRPC call so an unhealthy client-service
// can't block the (already-committed) account operation indefinitely. The
// account write has already succeeded; notifications are best-effort side effects.
const emitTimeout = 5 * time.Second

// eventPublisher is the narrow Kafka surface AccountService needs to satisfy the
// "publish from the service layer" rule. Implemented by *kafkaprod.Producer.
type eventPublisher interface {
	PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error
	PublishAccountStatusChanged(ctx context.Context, msg kafkaprod.AccountStatusChangedMsg) error
	PublishAccountNameUpdated(ctx context.Context, msg kafkamsg.AccountNameUpdatedMessage) error
	PublishAccountLimitsUpdated(ctx context.Context, msg kafkamsg.AccountLimitsUpdatedMessage) error
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
	SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error
}

// clientLookup is the subset of the client-service gRPC client used to resolve a
// client's email for the account-created notification. Implemented by
// clientpb.ClientServiceClient.
type clientLookup interface {
	GetClient(ctx context.Context, in *clientpb.GetClientRequest, opts ...grpc.CallOption) (*clientpb.ClientResponse, error)
}

// clientReplicaReader is the local read-model the service consults before
// falling back to a synchronous GetClient (SP-1 hybrid lazy fallback).
type clientReplicaReader interface {
	GetByID(ctx context.Context, id uint64) (model.ClientReplica, error)
	Upsert(ctx context.Context, in model.ClientReplica) error
}

// WithEvents wires the Kafka producer so the service publishes its domain events
// itself (per CLAUDE.md, events are published from the service layer, not the
// handler). No-op publishing when nil. Returns the service for chaining.
func (s *AccountService) WithEvents(p eventPublisher) *AccountService {
	s.events = p
	return s
}

// WithClientLookup wires the client-service client used to fetch the owner's
// email for the account-created email. Returns the service for chaining.
func (s *AccountService) WithClientLookup(c clientLookup) *AccountService {
	s.clients = c
	return s
}

// WithClientReplica wires the local client replica read-model (SP-1).
// When set, resolveClientEmail reads the replica first and falls back to
// a single synchronous GetClient only on a cache miss. Returns the service for chaining.
func (s *AccountService) WithClientReplica(r clientReplicaReader) *AccountService {
	s.clientReplica = r
	return s
}

// resolveClientEmail returns the client's email from the local replica, falling
// back to a single synchronous GetClient on a miss and backfilling the replica
// (SP-1 hybrid lazy fallback). Returns "" only if both sources fail.
func (s *AccountService) resolveClientEmail(ctx context.Context, ownerID uint64) string {
	if s.clientReplica != nil {
		if rep, err := s.clientReplica.GetByID(ctx, ownerID); err == nil {
			return rep.Email
		}
	}
	if s.clients == nil {
		return ""
	}
	resp, err := s.clients.GetClient(ctx, &clientpb.GetClientRequest{Id: ownerID})
	if err != nil {
		log.Printf("warn: fetch client %d for account-created email: %v", ownerID, err)
		return ""
	}
	if s.clientReplica != nil {
		// ClientResponse has no Version; backfill at 0 so a later versioned
		// event overwrites it via the repo's version guard.
		_ = s.clientReplica.Upsert(ctx, model.ClientReplica{
			ID:        ownerID,
			Email:     resp.Email,
			FirstName: resp.FirstName,
			LastName:  resp.LastName,
			JMBG:      resp.Jmbg,
		})
	}
	return resp.Email
}

// resolveClientName returns the owner's display name ("First Last") from the
// local client replica, falling back to a single synchronous GetClient on a miss
// and backfilling the replica (SP-1 hybrid lazy fallback). Returns "" only if both
// sources fail — the account is still created (the denormalised name is a
// read-convenience, not an invariant). Used by CreateAccount so account reads
// expose the owner's name without a per-read cross-service lookup; previously
// client accounts were stored with an empty owner_name (only bank/state seed
// accounts set it), which made them look ownerless in the UI.
func (s *AccountService) resolveClientName(ctx context.Context, ownerID uint64) string {
	if s.clientReplica != nil {
		if rep, err := s.clientReplica.GetByID(ctx, ownerID); err == nil {
			if n := strings.TrimSpace(rep.FirstName + " " + rep.LastName); n != "" {
				return n
			}
		}
	}
	if s.clients == nil {
		return ""
	}
	resp, err := s.clients.GetClient(ctx, &clientpb.GetClientRequest{Id: ownerID})
	if err != nil {
		log.Printf("warn: fetch client %d for account owner name: %v", ownerID, err)
		return ""
	}
	if s.clientReplica != nil {
		_ = s.clientReplica.Upsert(ctx, model.ClientReplica{
			ID: ownerID, Email: resp.Email, FirstName: resp.FirstName, LastName: resp.LastName, JMBG: resp.Jmbg,
		})
	}
	return strings.TrimSpace(resp.FirstName + " " + resp.LastName)
}

// hasHumanOwner reports whether the account belongs to a real client (not a
// bank/state-owned sentinel account), i.e. whether in-app/email notifications
// should be emitted.
func hasHumanOwner(a *model.Account) bool {
	return !a.IsBankAccount && a.OwnerID != BankOwnerID && a.OwnerID != StateOwnerID
}

// emitAccountCreated publishes the account-created domain event, plus (for
// client-owned accounts) an in-app notification and an activation/welcome email.
// Best-effort: the account write has already committed.
func (s *AccountService) emitAccountCreated(account *model.Account) {
	if s.events == nil {
		return
	}
	bg := context.Background()
	_ = s.events.PublishAccountCreated(bg, kafkamsg.AccountCreatedMessage{
		AccountNumber: account.AccountNumber,
		OwnerID:       account.OwnerID,
		AccountKind:   account.AccountKind,
		CurrencyCode:  account.CurrencyCode,
	})
	if !hasHumanOwner(account) {
		return
	}
	_ = s.events.PublishGeneralNotification(bg, kafkamsg.GeneralNotificationMessage{
		UserID:  account.OwnerID,
		Type:    "ACCOUNT_OPENED",
		Data:    map[string]string{"account_number": account.AccountNumber, "currency": account.CurrencyCode},
		RefType: "account",
		RefID:   account.ID,
	})
	// Welcome email — best effort; needs the owner's email.
	// Replica-first (SP-1); falls back to synchronous GetClient on miss.
	if s.clients == nil && s.clientReplica == nil {
		return
	}
	ctx, cancel := context.WithTimeout(bg, emitTimeout)
	defer cancel()
	email := s.resolveClientEmail(ctx, account.OwnerID)
	if email == "" {
		return
	}
	if err := s.events.SendEmail(bg, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailTypeAccountCreated,
		Data: map[string]string{
			"account_number": account.AccountNumber,
			"account_name":   account.AccountName,
			"currency":       account.CurrencyCode,
		},
	}); err != nil {
		log.Printf("warn: send account-created email to %s: %v", email, err)
	}
}

// emitAccountNameUpdated publishes the name-updated domain event (+ notification).
func (s *AccountService) emitAccountNameUpdated(account *model.Account, newName string) {
	if s.events == nil {
		return
	}
	bg := context.Background()
	_ = s.events.PublishAccountNameUpdated(bg, kafkamsg.AccountNameUpdatedMessage{
		AccountID:     account.ID,
		AccountNumber: account.AccountNumber,
		NewName:       newName,
	})
	if !hasHumanOwner(account) {
		return
	}
	_ = s.events.PublishGeneralNotification(bg, kafkamsg.GeneralNotificationMessage{
		UserID:  account.OwnerID,
		Type:    "ACCOUNT_NAME_UPDATED",
		Data:    map[string]string{"account_number": account.AccountNumber, "new_name": newName},
		RefType: "account",
		RefID:   account.ID,
	})
}

// emitAccountLimitsUpdated publishes the limits-updated domain event (+ notification).
func (s *AccountService) emitAccountLimitsUpdated(account *model.Account, daily, monthly string) {
	if s.events == nil {
		return
	}
	bg := context.Background()
	_ = s.events.PublishAccountLimitsUpdated(bg, kafkamsg.AccountLimitsUpdatedMessage{
		AccountID:     account.ID,
		AccountNumber: account.AccountNumber,
		DailyLimit:    daily,
		MonthlyLimit:  monthly,
	})
	if !hasHumanOwner(account) {
		return
	}
	_ = s.events.PublishGeneralNotification(bg, kafkamsg.GeneralNotificationMessage{
		UserID:  account.OwnerID,
		Type:    "ACCOUNT_LIMITS_UPDATED",
		Data:    map[string]string{"account_number": account.AccountNumber, "daily_limit": daily, "monthly_limit": monthly},
		RefType: "account",
		RefID:   account.ID,
	})
}

// emitAccountStatusChanged publishes the status-changed domain event (+ notification).
func (s *AccountService) emitAccountStatusChanged(account *model.Account, newStatus string) {
	if s.events == nil {
		return
	}
	bg := context.Background()
	_ = s.events.PublishAccountStatusChanged(bg, kafkaprod.AccountStatusChangedMsg{
		AccountNumber: account.AccountNumber,
		Status:        newStatus,
	})
	if !hasHumanOwner(account) {
		return
	}
	_ = s.events.PublishGeneralNotification(bg, kafkamsg.GeneralNotificationMessage{
		UserID:  account.OwnerID,
		Type:    "ACCOUNT_STATUS_CHANGED",
		Data:    map[string]string{"account_number": account.AccountNumber, "new_status": newStatus},
		RefType: "account",
		RefID:   account.ID,
	})
}
