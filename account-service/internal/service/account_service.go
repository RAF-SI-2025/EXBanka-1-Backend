package service

import (
	"errors"
	"time"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
)

type AccountService struct {
	repo *repository.AccountRepository
}

func NewAccountService(repo *repository.AccountRepository) *AccountService {
	return &AccountService{repo: repo}
}

func maintenanceFeeByType(accountType string) float64 {
	switch accountType {
	case "premium":
		return 500
	case "student":
		return 0
	case "youth":
		return 0
	case "pension":
		return 100
	default:
		return 220
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
		return errors.New("account_kind must be 'current' or 'foreign'")
	}

	account.AccountNumber = GenerateAccountNumber()
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
	return s.repo.UpdateName(id, clientID, newName)
}

func (s *AccountService) UpdateAccountLimits(id uint64, dailyLimit, monthlyLimit *float64) error {
	updates := make(map[string]interface{})
	if dailyLimit != nil {
		if *dailyLimit <= 0 {
			return errors.New("daily_limit must be greater than 0")
		}
		updates["daily_limit"] = *dailyLimit
	}
	if monthlyLimit != nil {
		if *monthlyLimit <= 0 {
			return errors.New("monthly_limit must be greater than 0")
		}
		updates["monthly_limit"] = *monthlyLimit
	}
	if len(updates) == 0 {
		return nil
	}
	return s.repo.UpdateLimits(id, updates)
}

func (s *AccountService) UpdateAccountStatus(id uint64, status string) error {
	if status != "active" && status != "inactive" {
		return errors.New("status must be 'active' or 'inactive'")
	}
	return s.repo.UpdateStatus(id, status)
}

func (s *AccountService) UpdateBalance(accountNumber string, amount float64, updateAvailable bool) error {
	return s.repo.UpdateBalance(accountNumber, amount, updateAvailable)
}
