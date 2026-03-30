package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

// BankOwnerID is the well-known owner ID for bank-owned accounts.
const BankOwnerID uint64 = 1_000_000_000

// StateOwnerID is the well-known owner ID for the state (government) entity.
const StateOwnerID uint64 = 2_000_000_000

type AccountService struct {
	repo *repository.AccountRepository
}

func NewAccountService(repo *repository.AccountRepository) *AccountService {
	return &AccountService{repo: repo}
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
		return errors.New("owner_id is required")
	}
	if account.CurrencyCode == "" {
		return errors.New("currency_code is required")
	}
	if account.AccountKind != "current" && account.AccountKind != "foreign" {
		return fmt.Errorf("account kind must be 'current' or 'foreign'; got: %s", account.AccountKind)
	}
	if account.AccountKind == "current" && account.CurrencyCode != "RSD" {
		return fmt.Errorf("current accounts can only use RSD currency; got: %s", account.CurrencyCode)
	}
	if account.AccountKind == "foreign" && account.CurrencyCode == "RSD" {
		return errors.New("foreign accounts cannot use RSD; supported currencies: EUR, CHF, USD, GBP, JPY, CAD, AUD")
	}

	// Check for duplicate account name for the same owner.
	if account.AccountName != "" {
		exists, err := s.repo.ExistsByNameAndOwner(account.AccountName, account.OwnerID, 0)
		if err != nil {
			return fmt.Errorf("failed to check account name uniqueness: %w", err)
		}
		if exists {
			return fmt.Errorf("an account with name %q already exists for this client", account.AccountName)
		}
	}

	account.AccountNumber = GenerateAccountNumber(account.AccountKind)
	account.ExpiresAt = time.Now().AddDate(5, 0, 0)
	account.Status = "active"
	account.MaintenanceFee = maintenanceFeeByType(account.AccountType)

	return s.repo.Create(account)
}

func (s *AccountService) GetAccount(id uint64) (*model.Account, error) {
	return s.repo.GetByID(id)
}

func (s *AccountService) GetAccountByNumber(accountNumber string) (*model.Account, error) {
	return s.repo.GetByNumber(accountNumber)
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

func (s *AccountService) UpdateAccountName(id, clientID uint64, newName string) error {
	// Check for duplicate account name for the same owner.
	exists, err := s.repo.ExistsByNameAndOwner(newName, clientID, id)
	if err != nil {
		return fmt.Errorf("failed to check account name uniqueness: %w", err)
	}
	if exists {
		return fmt.Errorf("an account with name %q already exists for this client", newName)
	}
	return s.repo.UpdateName(id, clientID, newName)
}

func (s *AccountService) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *string) error {
	updates := make(map[string]interface{})
	if dailyLimit != nil && *dailyLimit != "" {
		d, err := decimal.NewFromString(*dailyLimit)
		if err != nil {
			return errors.New("invalid daily_limit value")
		}
		if d.IsNegative() || d.IsZero() {
			return errors.New("daily_limit must be greater than 0")
		}
		updates["daily_limit"] = d
	}
	if monthlyLimit != nil && *monthlyLimit != "" {
		m, err := decimal.NewFromString(*monthlyLimit)
		if err != nil {
			return errors.New("invalid monthly_limit value")
		}
		if m.IsNegative() || m.IsZero() {
			return errors.New("monthly_limit must be greater than 0")
		}
		updates["monthly_limit"] = m
	}
	if len(updates) == 0 {
		return nil
	}
	return s.repo.UpdateLimits(id, updates)
}

func (s *AccountService) UpdateAccountStatus(id uint64, newStatus string) error {
	if newStatus != "active" && newStatus != "inactive" {
		return fmt.Errorf("account status must be 'active' or 'inactive'; got: %s", newStatus)
	}

	account, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("account %d not found", id)
	}

	if account.Status == newStatus {
		return fmt.Errorf("account %d is already %s", id, newStatus)
	}

	return s.repo.UpdateStatus(id, newStatus)
}

func (s *AccountService) UpdateBalance(accountNumber string, amount decimal.Decimal, updateAvailable bool) error {
	// All checks (funds, spending limits) and updates (balance, spending) are
	// performed atomically inside a single SELECT FOR UPDATE transaction in the repo.
	return s.repo.UpdateBalance(accountNumber, amount, updateAvailable)
}

func (s *AccountService) CreateBankAccount(currencyCode, accountKind, accountName string) (*model.Account, error) {
	if accountKind != "current" && accountKind != "foreign" {
		return nil, fmt.Errorf("account kind must be 'current' or 'foreign'; got: %s", accountKind)
	}
	if currencyCode == "" {
		return nil, errors.New("currency_code is required")
	}
	// Check for duplicate account name for the bank owner.
	if accountName != "" {
		exists, err := s.repo.ExistsByNameAndOwner(accountName, BankOwnerID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to check account name uniqueness: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("an account with name %q already exists for this client", accountName)
		}
	}

	account := &model.Account{
		OwnerID:       BankOwnerID,
		OwnerName:     "EX Banka",
		AccountName:   accountName,
		CurrencyCode:  currencyCode,
		AccountKind:   accountKind,
		AccountType:   "bank",
		IsBankAccount: true,
	}
	account.AccountNumber = GenerateAccountNumber(account.AccountKind)
	account.ExpiresAt = time.Now().AddDate(50, 0, 0) // 50-year expiry for bank accounts
	account.Status = "active"
	account.MaintenanceFee = decimal.Zero
	return account, s.repo.Create(account)
}

func (s *AccountService) ListBankAccounts() ([]model.Account, error) {
	return s.repo.ListBankAccounts()
}

func (s *AccountService) DeleteBankAccount(id uint64) error {
	account, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	if !account.IsBankAccount {
		return fmt.Errorf("account %d is not a bank account", id)
	}
	bankAccounts, err := s.repo.ListBankAccounts()
	if err != nil {
		return err
	}
	rsdCount := 0
	foreignCount := 0
	for _, a := range bankAccounts {
		if a.ID == id {
			continue
		}
		if a.CurrencyCode == "RSD" {
			rsdCount++
		} else {
			foreignCount++
		}
	}
	if account.CurrencyCode == "RSD" && rsdCount == 0 {
		return errors.New("cannot delete: bank must maintain at least one RSD account")
	}
	if account.CurrencyCode != "RSD" && foreignCount == 0 {
		return errors.New("cannot delete: bank must maintain at least one foreign currency account")
	}
	return s.repo.SoftDelete(id)
}

func (s *AccountService) GetBankRSDAccount() (*model.Account, error) {
	accounts, err := s.repo.ListBankAccountsByCurrency("RSD")
	if err != nil || len(accounts) == 0 {
		return nil, errors.New("no bank RSD account found")
	}
	return &accounts[0], nil
}

// UpdateSpending increments daily_spending and monthly_spending by amount on client accounts.
func (s *AccountService) UpdateSpending(accountNumber string, amount decimal.Decimal) error {
	return s.repo.UpdateSpending(accountNumber, amount)
}
