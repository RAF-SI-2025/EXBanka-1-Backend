package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

type PortfolioService struct {
	holdingRepo     HoldingRepo
	capitalGainRepo CapitalGainRepo
	listingRepo     ListingRepo
	stockRepo       StockRepo
	optionRepo      OptionRepo
	accountClient   accountpb.AccountServiceClient
	nameResolver    UserNameResolver
	stateAccountNo  string
}

func NewPortfolioService(
	holdingRepo HoldingRepo,
	capitalGainRepo CapitalGainRepo,
	listingRepo ListingRepo,
	stockRepo StockRepo,
	optionRepo OptionRepo,
	accountClient accountpb.AccountServiceClient,
	nameResolver UserNameResolver,
	stateAccountNo string,
) *PortfolioService {
	return &PortfolioService{
		holdingRepo:     holdingRepo,
		capitalGainRepo: capitalGainRepo,
		listingRepo:     listingRepo,
		stockRepo:       stockRepo,
		optionRepo:      optionRepo,
		accountClient:   accountClient,
		nameResolver:    nameResolver,
		stateAccountNo:  stateAccountNo,
	}
}

// ProcessBuyFill handles a buy order fill: creates/updates holding, debits account.
func (s *PortfolioService) ProcessBuyFill(order *model.Order, txn *model.OrderTransaction) error {
	// Look up user name for new holdings
	firstName, lastName := "", ""
	if s.nameResolver != nil {
		fn, ln, err := s.nameResolver(order.UserID, order.SystemType)
		if err == nil {
			firstName, lastName = fn, ln
		}
	}

	// Look up listing for security info
	listing, err := s.listingRepo.GetByID(order.ListingID)
	if err != nil {
		return err
	}

	// Look up security name
	securityName := order.Ticker
	if order.SecurityType == "stock" {
		if stock, err := s.stockRepo.GetByID(listing.SecurityID); err == nil {
			securityName = stock.Name
		}
	}

	holding := &model.Holding{
		UserID:        order.UserID,
		SystemType:    order.SystemType,
		UserFirstName: firstName,
		UserLastName:  lastName,
		SecurityType:  order.SecurityType,
		SecurityID:    listing.SecurityID,
		ListingID:     order.ListingID,
		Ticker:        order.Ticker,
		Name:          securityName,
		Quantity:      txn.Quantity,
		AveragePrice:  txn.PricePerUnit,
		AccountID:     order.AccountID,
	}

	if err := s.holdingRepo.Upsert(holding); err != nil {
		return err
	}

	// Debit buyer's account: total_price + proportional commission
	proportionalCommission := order.Commission.Mul(
		decimal.NewFromInt(txn.Quantity),
	).Div(decimal.NewFromInt(order.Quantity))
	debitAmount := txn.TotalPrice.Add(proportionalCommission)

	if err := s.debitAccount(order.AccountID, debitAmount); err != nil {
		return err
	}

	// Credit bank account with commission
	if err := s.creditBankCommission(proportionalCommission); err != nil {
		return err
	}

	return nil
}

// ProcessSellFill handles a sell order fill: decreases holding, credits account, records gain.
func (s *PortfolioService) ProcessSellFill(order *model.Order, txn *model.OrderTransaction) error {
	// Find the holding
	listing, err := s.listingRepo.GetByID(order.ListingID)
	if err != nil {
		return err
	}

	holding, err := s.holdingRepo.GetByUserAndSecurity(
		order.UserID, order.SecurityType, listing.SecurityID, order.AccountID,
	)
	if err != nil {
		return errors.New("holding not found for sell order")
	}

	if holding.Quantity < txn.Quantity {
		return errors.New("insufficient holding quantity for sell")
	}

	// Record capital gain
	gain := txn.PricePerUnit.Sub(holding.AveragePrice).Mul(decimal.NewFromInt(txn.Quantity))
	capitalGain := &model.CapitalGain{
		UserID:             order.UserID,
		SystemType:         order.SystemType,
		OrderTransactionID: txn.ID,
		OTC:                false,
		SecurityType:       order.SecurityType,
		Ticker:             order.Ticker,
		Quantity:           txn.Quantity,
		BuyPricePerUnit:    holding.AveragePrice,
		SellPricePerUnit:   txn.PricePerUnit,
		TotalGain:          gain,
		Currency:           listing.Exchange.Currency,
		AccountID:          order.AccountID,
		TaxYear:            time.Now().Year(),
		TaxMonth:           int(time.Now().Month()),
	}
	if err := s.capitalGainRepo.Create(capitalGain); err != nil {
		return err
	}

	// Decrease holding
	holding.Quantity -= txn.Quantity
	if holding.PublicQuantity > holding.Quantity {
		holding.PublicQuantity = holding.Quantity
	}

	if holding.Quantity == 0 {
		if err := s.holdingRepo.Delete(holding.ID); err != nil {
			return err
		}
	} else {
		if err := s.holdingRepo.Update(holding); err != nil {
			return err
		}
	}

	// Credit seller's account: total_price - proportional commission
	proportionalCommission := order.Commission.Mul(
		decimal.NewFromInt(txn.Quantity),
	).Div(decimal.NewFromInt(order.Quantity))
	creditAmount := txn.TotalPrice.Sub(proportionalCommission)

	if err := s.creditAccount(order.AccountID, creditAmount); err != nil {
		return err
	}

	// Credit bank with commission
	if err := s.creditBankCommission(proportionalCommission); err != nil {
		return err
	}

	return nil
}

// ListHoldings returns a user's holdings with computed profit.
func (s *PortfolioService) ListHoldings(userID uint64, filter HoldingFilter) ([]model.Holding, int64, error) {
	return s.holdingRepo.ListByUser(userID, filter)
}

// GetCurrentPrice retrieves current listing price for a holding.
func (s *PortfolioService) GetCurrentPrice(listingID uint64) (decimal.Decimal, error) {
	listing, err := s.listingRepo.GetByID(listingID)
	if err != nil {
		return decimal.Zero, err
	}
	return listing.Price, nil
}

// MakePublic sets a number of shares as publicly available for OTC trading.
func (s *PortfolioService) MakePublic(holdingID, userID uint64, quantity int64) (*model.Holding, error) {
	holding, err := s.holdingRepo.GetByID(holdingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("holding not found")
		}
		return nil, err
	}
	if holding.UserID != userID {
		return nil, errors.New("holding does not belong to user")
	}
	if holding.SecurityType != "stock" {
		return nil, errors.New("only stocks can be made public for OTC trading")
	}
	if quantity < 0 || quantity > holding.Quantity {
		return nil, errors.New("invalid public quantity")
	}

	holding.PublicQuantity = quantity

	if err := s.holdingRepo.Update(holding); err != nil {
		return nil, err
	}
	return holding, nil
}

// ExerciseOption exercises an option holding if it's in the money and not expired.
func (s *PortfolioService) ExerciseOption(holdingID, userID uint64) (*ExerciseResult, error) {
	holding, err := s.holdingRepo.GetByID(holdingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("holding not found")
		}
		return nil, err
	}
	if holding.UserID != userID {
		return nil, errors.New("holding does not belong to user")
	}
	if holding.SecurityType != "option" {
		return nil, errors.New("holding is not an option")
	}

	// Look up the option to get strike, type, settlement, stock info
	option, err := s.optionRepo.GetByID(holding.SecurityID)
	if err != nil {
		return nil, errors.New("option not found")
	}

	// Check settlement date hasn't passed
	if time.Now().After(option.SettlementDate) {
		return nil, errors.New("option has expired (settlement date passed)")
	}

	// Look up current stock price via listing
	stockListing, err := s.listingRepo.GetBySecurityIDAndType(option.StockID, "stock")
	if err != nil {
		return nil, errors.New("stock listing not found for option's underlying")
	}

	sharesAffected := holding.Quantity * 100 // 1 option = 100 shares
	var profit decimal.Decimal

	if option.OptionType == "call" {
		// CALL: stock price must be > strike price
		if stockListing.Price.LessThanOrEqual(option.StrikePrice) {
			return nil, errors.New("call option is not in the money")
		}
		profit = stockListing.Price.Sub(option.StrikePrice).Mul(decimal.NewFromInt(sharesAffected))

		// Debit account by strike × shares (buying stock at strike price)
		debitAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.debitAccount(holding.AccountID, debitAmount); err != nil {
			return nil, err
		}

		// Create/update stock holding at strike price
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			// Compensate: re-credit the debit amount
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}
		stockHolding := &model.Holding{
			UserID:        userID,
			SystemType:    holding.SystemType,
			UserFirstName: holding.UserFirstName,
			UserLastName:  holding.UserLastName,
			SecurityType:  "stock",
			SecurityID:    option.StockID,
			ListingID:     stockListing.ID,
			Ticker:        stock.Ticker,
			Name:          stock.Name,
			Quantity:      sharesAffected,
			AveragePrice:  option.StrikePrice,
			AccountID:     holding.AccountID,
		}
		if err := s.holdingRepo.Upsert(stockHolding); err != nil {
			// Compensate: re-credit the debit amount
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}

	} else { // "put"
		// PUT: stock price must be < strike price
		if stockListing.Price.GreaterThanOrEqual(option.StrikePrice) {
			return nil, errors.New("put option is not in the money")
		}
		profit = option.StrikePrice.Sub(stockListing.Price).Mul(decimal.NewFromInt(sharesAffected))

		// User must hold enough stock
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			return nil, err
		}
		stockHolding, err := s.holdingRepo.GetByUserAndSecurity(userID, "stock", option.StockID, holding.AccountID)
		if err != nil || stockHolding.Quantity < sharesAffected {
			return nil, errors.New("insufficient stock holdings to exercise put option")
		}

		// Credit account by strike × shares (selling stock at strike price)
		creditAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.creditAccount(holding.AccountID, creditAmount); err != nil {
			return nil, err
		}

		// Record the average price before modifying stockHolding for capital gain calculation
		avgPriceBeforeUpdate := stockHolding.AveragePrice

		// Decrease stock holding
		stockHolding.Quantity -= sharesAffected
		if stockHolding.PublicQuantity > stockHolding.Quantity {
			stockHolding.PublicQuantity = stockHolding.Quantity
		}

		if stockHolding.Quantity == 0 {
			if err := s.holdingRepo.Delete(stockHolding.ID); err != nil {
				// Compensate: re-debit the credit amount
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		} else {
			if err := s.holdingRepo.Update(stockHolding); err != nil {
				// Compensate: re-debit the credit amount
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		}

		// Record capital gain for the stock sale
		gain := option.StrikePrice.Sub(avgPriceBeforeUpdate).Mul(decimal.NewFromInt(sharesAffected))
		capitalGain := &model.CapitalGain{
			UserID:           userID,
			SystemType:       holding.SystemType,
			SecurityType:     "stock",
			Ticker:           stock.Ticker,
			Quantity:         sharesAffected,
			BuyPricePerUnit:  avgPriceBeforeUpdate,
			SellPricePerUnit: option.StrikePrice,
			TotalGain:        gain,
			Currency:         stockListing.Exchange.Currency,
			AccountID:        holding.AccountID,
			TaxYear:          time.Now().Year(),
			TaxMonth:         int(time.Now().Month()),
		}
		if err := s.capitalGainRepo.Create(capitalGain); err != nil {
			// Compensate: re-debit the credit amount
			_ = s.debitAccount(holding.AccountID, creditAmount)
			return nil, err
		}
	}

	// Delete option holding
	if err := s.holdingRepo.Delete(holdingID); err != nil {
		return nil, err
	}

	return &ExerciseResult{
		ID:                holdingID,
		OptionTicker:      holding.Ticker,
		ExercisedQuantity: holding.Quantity,
		SharesAffected:    sharesAffected,
		Profit:            profit,
	}, nil
}

// ExerciseOptionByOptionID exercises an option identified by its option_id.
// When holdingID > 0, the specified holding is exercised directly (delegates
// to ExerciseOption). When holdingID == 0, the user's oldest long holding for
// the given optionID is auto-resolved and exercised.
func (s *PortfolioService) ExerciseOptionByOptionID(ctx context.Context, optionID, userID, holdingID uint64) (*ExerciseResult, error) {
	if holdingID > 0 {
		return s.ExerciseOption(holdingID, userID)
	}

	// Auto-resolve: find the user's oldest long holding on this option.
	holding, err := s.holdingRepo.FindOldestLongOptionHolding(userID, optionID)
	if err != nil {
		return nil, err
	}
	if holding == nil {
		return nil, errors.New("option holding not found")
	}
	return s.ExerciseOption(holding.ID, userID)
}

// --- Account helpers ---

func (s *PortfolioService) debitAccount(accountID uint64, amount decimal.Decimal) error {
	acctResp, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return err
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   acctResp.AccountNumber,
		Amount:          amount.Neg().StringFixed(4), // negative for debit
		UpdateAvailable: true,
	})
	return err
}

func (s *PortfolioService) creditAccount(accountID uint64, amount decimal.Decimal) error {
	acctResp, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return err
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   acctResp.AccountNumber,
		Amount:          amount.StringFixed(4), // positive for credit
		UpdateAvailable: true,
	})
	return err
}

func (s *PortfolioService) creditBankCommission(commission decimal.Decimal) error {
	if commission.IsZero() {
		return nil
	}
	_, err := s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   s.stateAccountNo,
		Amount:          commission.StringFixed(4),
		UpdateAvailable: true,
	})
	return err
}

// --- Types ---

type ExerciseResult struct {
	ID                uint64
	OptionTicker      string
	ExercisedQuantity int64
	SharesAffected    int64
	Profit            decimal.Decimal
}
