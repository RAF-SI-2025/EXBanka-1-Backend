package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/cache"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

const accountCacheTTL = 2 * time.Minute

// BankOwnerID is the well-known owner ID for bank-owned accounts.
const BankOwnerID uint64 = 1_000_000_000

// StateOwnerID is the well-known owner ID for the state (government) entity.
const StateOwnerID uint64 = 2_000_000_000

type AccountService struct {
	repo          *repository.AccountRepository
	changelogRepo *repository.ChangelogRepository
	bankRepo      *repository.BankAccountRepository
	db            *gorm.DB
	cache         *cache.RedisCache
	events        eventPublisher      // optional; nil → no event publishing
	clients       clientLookup        // optional; nil → skip account-created email (gRPC fallback)
	clientReplica clientReplicaReader // optional; nil → skip replica lookup (SP-1)
}

func NewAccountService(repo *repository.AccountRepository, db *gorm.DB, redisCache *cache.RedisCache, changelogRepo ...*repository.ChangelogRepository) *AccountService {
	svc := &AccountService{repo: repo, db: db, cache: redisCache}
	if len(changelogRepo) > 0 {
		svc.changelogRepo = changelogRepo[0]
	}
	return svc
}

// SetBankRepo wires the BankAccountRepository after construction. Used by
// cmd/main.go to avoid changing the variadic NewAccountService signature.
func (s *AccountService) SetBankRepo(bankRepo *repository.BankAccountRepository) {
	s.bankRepo = bankRepo
}

func maintenanceFeeByType(accountType string) decimal.Decimal {
	switch accountType {
	case "premium":
		return decimal.NewFromInt(500)
	case "student":
		return decimal.Zero
	case "youth":
		return decimal.Zero
	case "pension":
		return decimal.NewFromInt(100)
	default:
		return decimal.NewFromInt(220)
	}
}

func (s *AccountService) CreateAccount(account *model.Account) error {
	if account.OwnerID == 0 {
		return fmt.Errorf("CreateAccount: owner_id is required: %w", ErrInvalidAccount)
	}
	if account.CurrencyCode == "" {
		return fmt.Errorf("CreateAccount: currency_code is required: %w", ErrInvalidAccount)
	}
	if account.AccountKind != "current" && account.AccountKind != "foreign" {
		return fmt.Errorf("CreateAccount: account kind must be 'current' or 'foreign'; got: %s: %w", account.AccountKind, ErrInvalidAccount)
	}
	if account.AccountKind == "current" && account.CurrencyCode != "RSD" {
		return fmt.Errorf("CreateAccount: current accounts can only use RSD currency; got: %s: %w", account.CurrencyCode, ErrInvalidAccount)
	}
	if account.AccountKind == "foreign" && account.CurrencyCode == "RSD" {
		return fmt.Errorf("CreateAccount: foreign accounts cannot use RSD; supported currencies: EUR, CHF, USD, GBP, JPY, CAD, AUD: %w", ErrInvalidAccount)
	}

	// Check for duplicate account name for the same owner.
	if account.AccountName != "" {
		exists, err := s.repo.ExistsByNameAndOwner(account.AccountName, account.OwnerID, 0)
		if err != nil {
			return fmt.Errorf("failed to check account name uniqueness: %w", err)
		}
		if exists {
			return fmt.Errorf("CreateAccount: an account with name %q already exists for this client: %w", account.AccountName, ErrAccountNameDuplicate)
		}
	}

	account.AccountNumber = GenerateAccountNumber(account.AccountKind)
	account.ExpiresAt = time.Now().AddDate(5, 0, 0)
	account.Status = "active"
	account.MaintenanceFee = maintenanceFeeByType(account.AccountType)

	// Denormalise the owner's display name onto the account so reads don't need a
	// per-account cross-service lookup. Only for real client owners (bank/state
	// accounts seed their own OwnerName) and only when the caller didn't already
	// supply one. Best-effort + time-bounded: a missing name never blocks or fails
	// the create (it just stays empty, as before). This fixes client accounts
	// being stored with an empty owner_name (they looked ownerless in the UI).
	if account.OwnerName == "" && hasHumanOwner(account) {
		nctx, cancel := context.WithTimeout(context.Background(), emitTimeout)
		if name := s.resolveClientName(nctx, account.OwnerID); name != "" {
			account.OwnerName = name
		}
		cancel()
	}

	if err := s.repo.Create(account); err != nil {
		return err
	}
	AccountsCreatedTotal.Inc()
	s.emitAccountCreated(account)
	return nil
}

func (s *AccountService) GetAccount(id uint64) (*model.Account, error) {
	ctx := context.Background()
	key := accountCacheKeyByID(id)

	if s.cache != nil {
		var cached model.Account
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, account, accountCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return account, nil
}

func (s *AccountService) GetAccountByNumber(accountNumber string) (*model.Account, error) {
	ctx := context.Background()
	key := accountCacheKeyByNumber(accountNumber)

	if s.cache != nil {
		var cached model.Account
		if err := s.cache.Get(ctx, key, &cached); err == nil {
			return &cached, nil
		}
	}

	account, err := s.repo.GetByNumber(accountNumber)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, key, account, accountCacheTTL); err != nil {
			log.Printf("warn: cache set failed for %s: %v", key, err)
		}
	}
	return account, nil
}

func (s *AccountService) ListAccountsByClient(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return s.repo.ListByClient(clientID, page, pageSize)
}

func (s *AccountService) ListAllAccounts(nameFilter, numberFilter, typeFilter string, page, pageSize int) ([]model.Account, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return s.repo.ListAll(nameFilter, numberFilter, typeFilter, page, pageSize)
}

func (s *AccountService) UpdateAccountName(id, clientID uint64, newName string, changedBy int64) error {
	// Fetch current state for changelog.
	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("UpdateAccountName(id=%d): %w", id, ErrAccountNotFound)
	}
	oldName := account.AccountName

	// Check for duplicate account name for the same owner.
	exists, err := s.repo.ExistsByNameAndOwner(newName, clientID, id)
	if err != nil {
		return fmt.Errorf("failed to check account name uniqueness: %w", err)
	}
	if exists {
		return fmt.Errorf("UpdateAccountName(id=%d): an account with name %q already exists for this client: %w", id, newName, ErrAccountNameDuplicate)
	}
	if err := s.repo.UpdateName(id, clientID, newName); err != nil {
		return err
	}
	s.invalidateAccountCache(id, account.AccountNumber)

	// Record changelog after successful mutation.
	entries := changelog.Diff("account", int64(id), changedBy, "", []changelog.FieldChange{
		{Field: "account_name", OldValue: oldName, NewValue: newName},
	})
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}
	s.emitAccountNameUpdated(account, newName)
	return nil
}

func (s *AccountService) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string, changedBy int64) error {
	// Fetch current state for changelog.
	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("UpdateAccountLimits(id=%d): %w", id, ErrAccountNotFound)
	}

	var changes []changelog.FieldChange
	updates := make(map[string]interface{})

	if dailyLimit != nil && *dailyLimit != "" {
		d, err := decimal.NewFromString(*dailyLimit)
		if err != nil {
			return fmt.Errorf("UpdateAccountLimits(id=%d): invalid daily_limit value: %w", id, ErrInvalidAccount)
		}
		if d.IsNegative() || d.IsZero() {
			return fmt.Errorf("UpdateAccountLimits(id=%d): daily_limit must be greater than 0: %w", id, ErrInvalidAccount)
		}
		changes = append(changes, changelog.FieldChange{
			Field: "daily_limit", OldValue: account.DailyLimit.String(), NewValue: d.String(),
		})
		updates["daily_limit"] = d
	}
	if monthlyLimit != nil && *monthlyLimit != "" {
		m, err := decimal.NewFromString(*monthlyLimit)
		if err != nil {
			return fmt.Errorf("UpdateAccountLimits(id=%d): invalid monthly_limit value: %w", id, ErrInvalidAccount)
		}
		if m.IsNegative() || m.IsZero() {
			return fmt.Errorf("UpdateAccountLimits(id=%d): monthly_limit must be greater than 0: %w", id, ErrInvalidAccount)
		}
		changes = append(changes, changelog.FieldChange{
			Field: "monthly_limit", OldValue: account.MonthlyLimit.String(), NewValue: m.String(),
		})
		updates["monthly_limit"] = m
	}
	if len(updates) == 0 {
		return nil
	}

	if err := s.repo.UpdateLimits(id, updates); err != nil {
		return err
	}
	s.invalidateAccountCache(id, account.AccountNumber)

	// Record changelog after successful mutation.
	entries := changelog.Diff("account", int64(id), changedBy, "", changes)
	if s.changelogRepo != nil && len(entries) > 0 {
		_ = s.changelogRepo.CreateBatch(entries)
	}

	// Reflect the new values on the in-memory account for the event (unchanged
	// limits keep their loaded value), then publish.
	if v, ok := updates["daily_limit"].(decimal.Decimal); ok {
		account.DailyLimit = v
	}
	if v, ok := updates["monthly_limit"].(decimal.Decimal); ok {
		account.MonthlyLimit = v
	}
	s.emitAccountLimitsUpdated(account, account.DailyLimit.StringFixed(2), account.MonthlyLimit.StringFixed(2))
	return nil
}

func (s *AccountService) UpdateAccountStatus(id uint64, newStatus string, changedBy int64) error {
	if newStatus != "active" && newStatus != "inactive" {
		return fmt.Errorf("UpdateAccountStatus(id=%d, status=%s): status must be 'active' or 'inactive': %w", id, newStatus, ErrInvalidStatus)
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("UpdateAccountStatus(id=%d): %w", id, ErrAccountNotFound)
	}

	oldStatus := account.Status
	if oldStatus == newStatus {
		return fmt.Errorf("UpdateAccountStatus(id=%d): account is already %s: %w", id, newStatus, ErrAccountInactive)
	}

	if err := s.repo.UpdateStatus(id, newStatus); err != nil {
		return err
	}
	AccountStatusChangesTotal.WithLabelValues(newStatus).Inc()
	s.invalidateAccountCache(id, account.AccountNumber)

	// Record changelog after successful mutation.
	if s.changelogRepo != nil {
		entry := changelog.NewStatusChangeEntry("account", int64(id), changedBy, oldStatus, newStatus, "")
		_ = s.changelogRepo.Create(entry)
	}
	s.emitAccountStatusChanged(account, newStatus)
	return nil
}

func (s *AccountService) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	return s.UpdateBalanceWithOpts(accountNumber, amount, updateAvailable, repository.UpdateBalanceOpts{})
}

// UpdateBalanceWithOpts is the memo/idempotency-aware entry point. Callers that
// pass a non-empty IdempotencyKey get "at-most-once" semantics: a retry with
// the same key short-circuits via the partial unique index on ledger_entries
// and returns success without mutating the account. Callers that pass a non-
// empty Memo get a ledger_entries row with that description so the balance
// change is visible in transaction history.
//
// Backward compatible: UpdateBalance with no opts remains pure balance
// mutation (no ledger row written), so existing transaction-service /
// payment-service flows that write their own ledger entries are unaffected.
func (s *AccountService) UpdateBalanceWithOpts(accountNumber string, amount decimal.Decimal, updateAvailable bool, opts repository.UpdateBalanceOpts) error {
	// All checks (funds, spending limits) and updates (balance, spending) are
	// performed atomically inside a single SELECT FOR UPDATE transaction in the repo.
	if _, err := s.repo.UpdateBalance(accountNumber, amount, updateAvailable, opts); err != nil {
		// Map repo failure modes onto coded sentinels so the caller gets a clean
		// 429/409/404 instead of a 500 leaking the account number / balances.
		switch {
		case errors.Is(err, repository.ErrSpendingLimit):
			return ErrSpendingLimitExceeded
		case errors.Is(err, repository.ErrInsufficientFunds):
			return ErrInsufficientBalance
		case errors.Is(err, gorm.ErrRecordNotFound):
			return ErrAccountNotFound
		default:
			return err
		}
	}
	// Invalidate cached read-only data after balance change. Resolve the id from
	// the number (cache-independent DB read) so BOTH cache keys — account:id:N
	// AND account:num:S — are dropped; otherwise a read by id (GET
	// /me/accounts/:id) keeps serving the pre-change balance. This is the path
	// the OTC premium credit / fee debit (CreditAccount/DebitAccount →
	// UpdateBalance gRPC) flow through.
	// NOTE: The authoritative balance check always uses SELECT FOR UPDATE in the
	// repo — this invalidation only affects display queries via GetAccount/GetAccountByNumber.
	var id uint64
	_ = s.db.Model(&model.Account{}).Select("id").Where("account_number = ?", accountNumber).Scan(&id).Error
	s.invalidateAccountCache(id, accountNumber)
	return nil
}

func (s *AccountService) CreateBankAccount(currencyCode, accountKind, accountName string, initialBalance decimal.Decimal) (*model.Account, error) {
	if accountKind != "current" && accountKind != "foreign" {
		return nil, fmt.Errorf("CreateBankAccount: account kind must be 'current' or 'foreign'; got: %s: %w", accountKind, ErrInvalidAccount)
	}
	if currencyCode == "" {
		return nil, fmt.Errorf("CreateBankAccount: currency_code is required: %w", ErrInvalidAccount)
	}
	// Check for duplicate account name for the bank owner.
	if accountName != "" {
		exists, err := s.repo.ExistsByNameAndOwner(accountName, BankOwnerID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to check account name uniqueness: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("CreateBankAccount: an account with name %q already exists for this client: %w", accountName, ErrAccountNameDuplicate)
		}
	}

	account := &model.Account{
		OwnerID:          BankOwnerID,
		OwnerName:        "EX Banka",
		AccountName:      accountName,
		CurrencyCode:     currencyCode,
		AccountKind:      accountKind,
		AccountType:      "bank",
		IsBankAccount:    true,
		Balance:          initialBalance,
		AvailableBalance: initialBalance,
	}
	account.AccountNumber = GenerateAccountNumber(account.AccountKind)
	account.ExpiresAt = time.Now().AddDate(50, 0, 0) // 50-year expiry for bank accounts
	account.Status = "active"
	account.MaintenanceFee = decimal.Zero
	if err := s.repo.Create(account); err != nil {
		return nil, err
	}
	AccountsCreatedTotal.Inc()
	// Bank accounts have no human owner, so emitAccountCreated publishes only the
	// AccountCreated domain event (the notification/email are skipped internally).
	s.emitAccountCreated(account)
	return account, nil
}

// SetAccountCategory stamps an account_category value onto an existing account
// row. Used by CreateBankAccount to propagate the optional category from the
// gRPC request after the base create has already committed.
func (s *AccountService) SetAccountCategory(accountID uint64, category string) error {
	return s.db.Model(&model.Account{}).
		Where("id = ?", accountID).
		Update("account_category", category).Error
}

func (s *AccountService) ListBankAccounts() ([]model.Account, error) {
	return s.repo.ListBankAccounts()
}

func (s *AccountService) DeleteBankAccount(id uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// Lock ALL bank accounts to prevent concurrent deletion races.
		// Two concurrent deletes could both see enough remaining accounts and
		// both succeed, violating the "at least 1 RSD + 1 foreign" constraint.
		var bankAccounts []model.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("is_bank_account = ? AND status != ?", true, "deactivated").
			Find(&bankAccounts).Error; err != nil {
			return err
		}

		var target *model.Account
		rsdCount, foreignCount := 0, 0
		for i := range bankAccounts {
			a := &bankAccounts[i]
			if a.ID == id {
				target = a
				continue
			}
			if a.CurrencyCode == "RSD" {
				rsdCount++
			} else {
				foreignCount++
			}
		}
		if target == nil {
			return fmt.Errorf("DeleteBankAccount(id=%d): %w", id, ErrAccountNotFound)
		}
		if !target.IsBankAccount {
			return fmt.Errorf("DeleteBankAccount(id=%d): account is not a bank account: %w", id, ErrInvalidAccount)
		}
		if target.CurrencyCode == "RSD" && rsdCount == 0 {
			return fmt.Errorf("DeleteBankAccount(id=%d): bank must maintain at least one RSD account: %w", id, ErrLastBankAccount)
		}
		if target.CurrencyCode != "RSD" && foreignCount == 0 {
			return fmt.Errorf("DeleteBankAccount(id=%d): bank must maintain at least one foreign currency account: %w", id, ErrLastBankAccount)
		}
		return tx.Delete(target).Error
	})
}

func (s *AccountService) GetBankRSDAccount() (*model.Account, error) {
	accounts, err := s.repo.ListBankAccountsByCurrency("RSD")
	if err != nil || len(accounts) == 0 {
		return nil, fmt.Errorf("GetBankRSDAccount: no bank RSD account found: %w", ErrAccountNotFound)
	}
	return &accounts[0], nil
}

// UpdateSpending increments daily_spending and monthly_spending by amount on client accounts.
func (s *AccountService) UpdateSpending(accountNumber string, amount decimal.Decimal) error {
	return s.repo.UpdateSpending(accountNumber, amount)
}

// ApplyClientLimitPolicy propagates a client's limit policy (SP-5) to every
// non-bank account the client owns, setting each account's DailyLimit/MonthlyLimit.
// Zero/non-positive policy values are skipped (UpdateAccountLimits rejects them).
// Idempotent: re-applying the same values is a no-op-ish safe write.
func (s *AccountService) ApplyClientLimitPolicy(ctx context.Context, clientID uint64, daily, monthly decimal.Decimal, changedBy int64) error {
	accounts, err := s.repo.ListNonBankByOwner(clientID)
	if err != nil {
		return fmt.Errorf("ApplyClientLimitPolicy(client=%d): list accounts: %w", clientID, err)
	}
	var dailyPtr, monthlyPtr *string
	if daily.IsPositive() {
		d := daily.StringFixed(4)
		dailyPtr = &d
	}
	if monthly.IsPositive() {
		m := monthly.StringFixed(4)
		monthlyPtr = &m
	}
	if dailyPtr == nil && monthlyPtr == nil {
		return nil // nothing positive to apply
	}
	var firstErr error
	for _, acct := range accounts {
		if err := s.UpdateAccountLimits(acct.ID, dailyPtr, monthlyPtr, changedBy); err != nil {
			log.Printf("ApplyClientLimitPolicy(client=%d, account=%d): %v", clientID, acct.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr // non-nil if any account failed → consumer retries (idempotent re-apply)
}

// invalidateAccountCache removes an account from Redis cache by ID and/or number.
func (s *AccountService) invalidateAccountCache(id uint64, accountNumber string) {
	evictAccountCache(s.cache, id, accountNumber)
}

// DebitBankAccount atomically debits the bank sentinel account for the given
// currency. Returns FailedPrecondition via the repository layer when liquidity
// is insufficient, or NotFound when no sentinel exists for the currency.
// Idempotent by reference.
func (s *AccountService) DebitBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid amount %q: %w", amountStr, err)
	}
	return s.bankRepo.DebitBank(ctx, currency, amount, reference, reason)
}

// CreditBankAccount atomically credits the bank sentinel account. Used by
// credit-service saga compensation when loan disbursement step B (borrower
// credit) fails after step A (bank debit) succeeded.
func (s *AccountService) CreditBankAccount(ctx context.Context, currency, amountStr, reference, reason string) (*repository.BankOpResult, error) {
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return nil, fmt.Errorf("invalid amount %q: %w", amountStr, err)
	}
	return s.bankRepo.CreditBank(ctx, currency, amount, reference, reason)
}
