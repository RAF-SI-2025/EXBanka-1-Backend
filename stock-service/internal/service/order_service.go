package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/shared/saga"
	stockgrpc "github.com/exbanka/stock-service/internal/grpc"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// --- Default tuning constants for placement reservations ---
//
// Defaults are applied when a particular setting is absent in the
// system_settings table. They intentionally err on the safe (larger)
// side so we never under-reserve funds for a buy order.

const (
	// defaultMarketSlippagePct is the worst-case price buffer added on top of
	// a market/stop order's native amount. 5% is a common industry default
	// for pre-trade margin on volatile securities.
	defaultMarketSlippagePct = 0.05
	// defaultCommissionRate is the commission-buffer percentage included in
	// the reserved amount. 0.25% covers the capped commissions used by
	// calculateCommission (14%/24% of notional capped at $7/$12) for typical
	// consumer order sizes.
	defaultCommissionRate = 0.0025
)

// OrderSettings exposes the knobs CreateOrder needs when computing a
// placement reservation. Implementations may back this with
// system_settings rows, config env vars, or hard-coded values in tests.
type OrderSettings interface {
	// CommissionRate returns the commission fraction (e.g. 0.0025 for 0.25%).
	CommissionRate() decimal.Decimal
	// MarketSlippagePct returns the market-order slippage buffer (e.g. 0.05
	// for 5%). Limit/stop-limit orders do not use this.
	MarketSlippagePct() decimal.Decimal
}

// AccountClientAPI is the subset of grpc.AccountClient that OrderService needs
// for placement. Defined as an interface so tests can stub it without
// constructing the real wrapper.
type AccountClientAPI interface {
	ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode string) (*accountpb.ReserveFundsResponse, error)
	ReleaseReservation(ctx context.Context, orderID uint64) (*accountpb.ReleaseReservationResponse, error)
	Stub() accountpb.AccountServiceClient
}

// ForexPairLookup is the narrow lookup OrderService needs to validate forex
// quote/base currencies at placement.
type ForexPairLookup interface {
	GetByID(id uint64) (*model.ForexPair, error)
}

// HoldingReservationAPI is the subset of HoldingReservationService OrderService
// needs to reserve shares on sell-side placement. Stated as an interface so
// tests can stub it without a real DB.
//
// Reserve's lookup key is (user_id, system_type, security_type, security_id) —
// matches the Part-A rollup where holdings aggregate across accounts.
type HoldingReservationAPI interface {
	Reserve(ctx context.Context, userID uint64, systemType, securityType string,
		securityID, orderID uint64, qty int64) (*ReserveHoldingResult, error)
	Release(ctx context.Context, orderID uint64) (*ReleaseHoldingResult, error)
}

// ActuaryClientAPI is the narrow interface OrderService uses to enforce
// per-actuary limits at placement/approve/cancel time. Tests stub this
// without a real gRPC stub.
type ActuaryClientAPI interface {
	GetActuaryLimit(ctx context.Context, employeeID uint64) (*stockgrpc.ActuaryLimitInfo, error)
	IncrementUsedLimit(ctx context.Context, actuaryID uint64, amountRSD decimal.Decimal) error
	DecrementUsedLimit(ctx context.Context, actuaryID uint64, amountRSD decimal.Decimal) error
}

type OrderService struct {
	orderRepo             OrderRepo
	txRepo                OrderTransactionRepo
	listingRepo           ListingRepo
	settingRepo           SettingRepo
	securityRepo          SecurityLookupRepo
	producer              OrderEventPublisher
	sagaRepo              SagaLogRepo
	accountClient         AccountClientAPI
	exchangeClient        exchangepb.ExchangeServiceClient
	holdingReservationSvc HoldingReservationAPI
	forexRepo             ForexPairLookup
	settings              OrderSettings
	// actuaryClient enforces per-actuary limits for employee-placed orders.
	// When nil (e.g., in older tests) limit enforcement is skipped — employee
	// orders behave like pre-fix (always auto-approved). Production wiring in
	// cmd/main.go always provides a real client.
	actuaryClient ActuaryClientAPI
	// fundRepo backs the on_behalf_of=fund branch of CreateOrder. Nil = the
	// branch rejects with FailedPrecondition. Wired via WithFundSupport.
	fundRepo FundLookup
}

// FundLookup is the narrow interface OrderService needs to validate
// on_behalf_of=fund placements. Implemented by *repository.FundRepository.
type FundLookup interface {
	GetByID(id uint64) (*model.InvestmentFund, error)
}

// WithFundSupport wires the fund repository so on_behalf_of=fund order
// placements are accepted. Without this builder the branch rejects.
func (s *OrderService) WithFundSupport(repo FundLookup) *OrderService {
	cp := *s
	cp.fundRepo = repo
	return &cp
}

// OrderEventPublisher abstracts Kafka event publishing for orders.
type OrderEventPublisher interface {
	PublishOrderCreated(ctx context.Context, msg interface{}) error
	PublishOrderApproved(ctx context.Context, msg interface{}) error
	PublishOrderDeclined(ctx context.Context, msg interface{}) error
	PublishOrderCancelled(ctx context.Context, msg interface{}) error
}

// SecurityLookupRepo provides settlement date + ticker lookups for the
// placement saga. Tests stub this without the underlying per-type repos.
type SecurityLookupRepo interface {
	GetFuturesSettlementDate(securityID uint64) (time.Time, error)
	// GetSecurityTicker returns the canonical ticker for a security. Callers
	// treat an empty string as "unknown — leave the order's ticker blank"
	// rather than failing.
	GetSecurityTicker(securityType string, securityID uint64) (string, error)
}

// NewOrderService constructs an OrderService with all placement-saga
// dependencies wired in. sagaRepo, accountClient, exchangeClient,
// holdingReservationSvc, forexRepo, and settings are new in Phase 2 — they
// back the placement saga introduced in Task 12 of the bank-safe settlement
// plan. settings may be nil in which case defaults are used.
func NewOrderService(
	orderRepo OrderRepo,
	txRepo OrderTransactionRepo,
	listingRepo ListingRepo,
	settingRepo SettingRepo,
	securityRepo SecurityLookupRepo,
	producer OrderEventPublisher,
	sagaRepo SagaLogRepo,
	accountClient AccountClientAPI,
	exchangeClient exchangepb.ExchangeServiceClient,
	holdingReservationSvc HoldingReservationAPI,
	forexRepo ForexPairLookup,
	settings OrderSettings,
) *OrderService {
	if settings == nil {
		settings = defaultOrderSettings{}
	}
	return &OrderService{
		orderRepo:             orderRepo,
		txRepo:                txRepo,
		listingRepo:           listingRepo,
		settingRepo:           settingRepo,
		securityRepo:          securityRepo,
		producer:              producer,
		sagaRepo:              sagaRepo,
		accountClient:         accountClient,
		exchangeClient:        exchangeClient,
		holdingReservationSvc: holdingReservationSvc,
		forexRepo:             forexRepo,
		settings:              settings,
	}
}

// WithActuaryClient attaches the user-service actuary client so employee
// orders are subject to per-actuary limit enforcement. Returning the
// receiver keeps the builder chainable; calling sites that don't need the
// actuary check (older tests, tools) can simply omit this step.
func (s *OrderService) WithActuaryClient(client ActuaryClientAPI) *OrderService {
	s.actuaryClient = client
	return s
}

// CreateOrderRequest is the input shape for the placement saga. Bundling
// fields into a struct keeps the CreateOrder call sites readable as we add
// forex-specific inputs (BaseAccountID) without bloating a positional
// signature.
type CreateOrderRequest struct {
	UserID           uint64
	SystemType       string
	ListingID        uint64
	HoldingID        *uint64
	Direction        string
	OrderType        string
	Quantity         int64
	LimitValue       *decimal.Decimal
	StopValue        *decimal.Decimal
	AllOrNone        bool
	Margin           bool
	AccountID        uint64
	ActingEmployeeID uint64
	// BaseAccountID is required for forex orders (direction must be "buy" for
	// forex); points at the user's base-currency account that will be
	// credited on fill. Must be nil for non-forex orders.
	BaseAccountID *uint64
	// OnBehalfOfFundID, when non-zero, places this order against the named
	// fund's RSD account. The owner becomes the bank sentinel and fills
	// credit fund_holdings. Caller must be the fund manager and AccountID
	// must equal fund.RSDAccountID. Requires WithFundSupport on the service.
	OnBehalfOfFundID uint64
}

// CreateOrder runs the Phase 2 placement saga. On success the returned
// order is in status="approved" with reservation metadata populated. On any
// saga-step failure the saga is unwound (compensations release any funds
// reserved and delete the pending-order row) and the original gRPC status
// error is returned to the caller.
//
// Steps (see SagaExecutor):
//
//  1. validate_listing
//  2. validate_direction (forex: buy only; requires BaseAccountID)
//  3. compute_reservation_amount (+ currency validation / conversion)
//  4. persist_order_pending
//  5. reserve_funds (buys only)
//  6. reserve_holding (non-forex sells only)
//  7. approve_order
//
// Commission and slippage come from the injected OrderSettings.
func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) (*model.Order, error) {
	// on_behalf_of=fund: validate + rewrite owner to the bank sentinel before
	// the saga runs. Resolves the fund and replaces (UserID, SystemType) with
	// (1_000_000_000, "employee") so reservation, fills, and approval flow as
	// a bank-side trade. Skipped (Forex base account, holding lookup) and
	// existing employee on-behalf-of-client logic are unaffected.
	var fundOrderID *uint64
	if req.OnBehalfOfFundID != 0 {
		if s.fundRepo == nil {
			return nil, status.Error(codes.FailedPrecondition, "fund support not configured")
		}
		fund, err := s.fundRepo.GetByID(req.OnBehalfOfFundID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "fund not found")
			}
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !fund.Active {
			return nil, status.Error(codes.FailedPrecondition, "fund is inactive")
		}
		if req.ActingEmployeeID == 0 {
			return nil, status.Error(codes.PermissionDenied, "fund orders require acting_employee_id")
		}
		if int64(req.ActingEmployeeID) != fund.ManagerEmployeeID {
			return nil, status.Error(codes.PermissionDenied, "fund_not_managed_by_actor")
		}
		if req.AccountID != fund.RSDAccountID {
			return nil, status.Error(codes.InvalidArgument, "account_id must equal fund RSD account")
		}
		req.UserID = 1_000_000_000
		req.SystemType = "employee"
		fid := fund.ID
		fundOrderID = &fid
	}

	sagaID := uuid.New().String()

	// Closure-shared state. The saga's Forward closures read/write these so
	// later steps can use values produced by earlier ones. The order's ID
	// is only known after persist_order_pending; that step writes it into
	// `state` so subsequent steps' saga_log rows carry the real order_id.
	var (
		listing         *model.Listing
		reserveAmount   decimal.Decimal
		reserveCurrency string
		placementRate   *decimal.Decimal
	)

	state := saga.NewState()
	state.Set("order_id", uint64(0))

	// --- pre-saga validation (no side effects, so kept out of the saga) ---
	l, err := s.listingRepo.GetByID(req.ListingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "listing not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	listing = l

	if listing.SecurityType == "forex" {
		if req.Direction != "buy" {
			return nil, status.Error(codes.InvalidArgument, "forex orders must be direction=buy")
		}
		if req.BaseAccountID == nil {
			return nil, status.Error(codes.InvalidArgument, "forex orders require base_account_id")
		}
	}
	if req.Direction != "buy" && req.Direction != "sell" {
		return nil, status.Error(codes.InvalidArgument, "direction must be buy or sell")
	}
	if (req.OrderType == "limit" || req.OrderType == "stop_limit") && req.LimitValue == nil {
		return nil, status.Error(codes.InvalidArgument, "limit_value required for limit/stop_limit orders")
	}
	if (req.OrderType == "stop" || req.OrderType == "stop_limit") && req.StopValue == nil {
		return nil, status.Error(codes.InvalidArgument, "stop_value required for stop/stop_limit orders")
	}

	// --- compute_reservation_amount + currency validation/conversion ---
	// For buys: native price × qty × contract size × (1+slippage if market) × (1+commission).
	// For sells: no funds reserved (sells produce funds on fill). Skip this step's
	// account-lookup side-effect and keep reserveAmount = 0.
	contractSize := contractSizeForSecurity(listing.SecurityType)
	pricePerUnit := listing.Price
	if req.OrderType == "limit" || req.OrderType == "stop_limit" {
		if req.LimitValue != nil {
			pricePerUnit = *req.LimitValue
		}
	} else if req.OrderType == "stop" {
		if req.StopValue != nil {
			pricePerUnit = *req.StopValue
		}
	}

	if req.Direction == "buy" {
		// compute_reservation_amount runs as a pre-saga step (pure read +
		// validation) so the resulting reserveAmount is captured before
		// any side-effecting steps run. Errors short-circuit before the
		// saga starts.
		if err := func() error {
			native, err := s.computeNativeReservation(req, listing, contractSize)
			if err != nil {
				return err
			}

			nativeCcy := listing.Exchange.Currency
			if listing.SecurityType == "forex" {
				// For forex, settle on the quote account; native is in the
				// pair's quote currency regardless of the Exchange.Currency.
				fp, ferr := s.lookupForexPair(listing)
				if ferr != nil {
					return ferr
				}
				nativeCcy = fp.QuoteCurrency
			}

			accountCcy, err := s.accountCurrency(ctx, req.AccountID)
			if err != nil {
				return err
			}

			if listing.SecurityType == "forex" {
				if accountCcy != nativeCcy {
					return status.Errorf(codes.InvalidArgument,
						"forex quote account currency mismatch: account=%s pair_quote=%s",
						accountCcy, nativeCcy)
				}
				baseCcy, err := s.accountCurrency(ctx, *req.BaseAccountID)
				if err != nil {
					return err
				}
				fp, ferr := s.lookupForexPair(listing)
				if ferr != nil {
					return ferr
				}
				if baseCcy != fp.BaseCurrency {
					return status.Errorf(codes.InvalidArgument,
						"forex base account currency mismatch: account=%s pair_base=%s",
						baseCcy, fp.BaseCurrency)
				}
				reserveAmount = native
				reserveCurrency = nativeCcy
				return nil
			}

			// Stocks / futures / options.
			if accountCcy == "" || accountCcy == nativeCcy {
				// If we couldn't resolve the account currency (older account
				// records without currency_code), assume same as listing
				// currency rather than blocking the order.
				reserveAmount = native
				if accountCcy != "" {
					reserveCurrency = accountCcy
				} else {
					reserveCurrency = nativeCcy
				}
				return nil
			}
			// Cross-currency: call exchange-service.Convert.
			resp, cerr := s.exchangeClient.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: nativeCcy,
				ToCurrency:   accountCcy,
				Amount:       native.StringFixed(8),
			})
			if cerr != nil {
				return cerr
			}
			conv, cerr2 := decimal.NewFromString(resp.ConvertedAmount)
			if cerr2 != nil {
				return status.Errorf(codes.Internal, "invalid converted amount: %v", cerr2)
			}
			rate, rerr := decimal.NewFromString(resp.EffectiveRate)
			if rerr == nil {
				placementRate = &rate
			}
			reserveAmount = conv
			reserveCurrency = accountCcy
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	// --- assemble the order struct (not yet persisted) ---
	commission := calculateCommission(req.OrderType, decimal.NewFromInt(contractSize).Mul(pricePerUnit).Mul(decimal.NewFromInt(req.Quantity)))

	afterHours := false
	if !s.isTestingMode() {
		afterHours = s.isAfterHours(listing)
	}

	// Resolve ticker from the security model so downstream Holding / order
	// response objects display it. Best-effort: a missing repo or lookup
	// error leaves Ticker blank rather than blocking placement.
	var orderTicker string
	if s.securityRepo != nil {
		if t, terr := s.securityRepo.GetSecurityTicker(listing.SecurityType, listing.SecurityID); terr == nil {
			orderTicker = t
		}
	}

	order := &model.Order{
		UserID:            req.UserID,
		SystemType:        req.SystemType,
		ListingID:         req.ListingID,
		HoldingID:         req.HoldingID,
		SecurityType:      listing.SecurityType,
		Ticker:            orderTicker,
		Direction:         req.Direction,
		OrderType:         req.OrderType,
		Quantity:          req.Quantity,
		ContractSize:      contractSize,
		PricePerUnit:      pricePerUnit,
		ApproximatePrice:  decimal.NewFromInt(contractSize).Mul(pricePerUnit).Mul(decimal.NewFromInt(req.Quantity)),
		Commission:        commission,
		LimitValue:        req.LimitValue,
		StopValue:         req.StopValue,
		Status:            "pending",
		ApprovedBy:        "",
		IsDone:            false,
		RemainingPortions: req.Quantity,
		AfterHours:        afterHours,
		AllOrNone:         req.AllOrNone,
		Margin:            req.Margin,
		AccountID:         req.AccountID,
		ActingEmployeeID:  req.ActingEmployeeID,
		BaseAccountID:     req.BaseAccountID,
		FundID:            fundOrderID,
		PlacementRate:     placementRate,
		SagaID:            sagaID,
		LastModification:  time.Now(),
	}
	if req.Direction == "buy" {
		amt := reserveAmount
		order.ReservationAmount = &amt
		order.ReservationCurrency = reserveCurrency
		acct := req.AccountID
		order.ReservationAccountID = &acct
	}

	// saga.Saga drives the side-effecting steps. Each step's Backward
	// undoes its forward effect; on any failure saga.Saga walks
	// completed steps in reverse and runs them automatically.
	//
	// The actuary-limit gate is computed inside the finalize step so the
	// needsApproval branch can choose between status="pending" and
	// status="approved" without splitting the saga in two.
	state.Set("step:persist_order_pending:amount", decimal.Zero)
	if req.Direction == "buy" {
		state.Set("step:reserve_funds:amount", reserveAmount)
		state.Set("step:reserve_funds:currency", reserveCurrency)
	}

	var (
		needsApproval  bool
		limitAmountRSD decimal.Decimal
		actuaryLimitID uint64
	)

	sg := saga.NewSaga(sagaID, stocksaga.NewRecorder(s.sagaRepo)).
		Add(saga.Step{
			Name: "persist_order_pending",
			Forward: func(ctx context.Context, st *saga.State) error {
				if err := s.orderRepo.Create(order); err != nil {
					return err
				}
				st.Set("order_id", order.ID)
				StockOrderTotal.WithLabelValues(req.OrderType, "pending").Inc()
				return nil
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				return s.orderRepo.Delete(order.ID)
			},
		}).
		AddIf(req.Direction == "buy", saga.Step{
			Name: "reserve_funds",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, rerr := s.accountClient.ReserveFunds(ctx, req.AccountID, order.ID, reserveAmount, reserveCurrency)
				return rerr
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, rerr := s.accountClient.ReleaseReservation(ctx, order.ID)
				return rerr
			},
		}).
		AddIf(req.Direction == "sell" && listing.SecurityType != "forex", saga.Step{
			Name: "reserve_holding",
			Forward: func(ctx context.Context, _ *saga.State) error {
				if s.holdingReservationSvc == nil {
					return status.Error(codes.Internal, "holding reservation service not configured")
				}
				_, herr := s.holdingReservationSvc.Reserve(ctx, req.UserID, req.SystemType,
					listing.SecurityType, listing.SecurityID, order.ID, req.Quantity)
				return herr
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				if s.holdingReservationSvc == nil {
					return nil
				}
				_, rerr := s.holdingReservationSvc.Release(ctx, order.ID)
				return rerr
			},
		}).
		Add(saga.Step{
			Name: "finalize_order",
			Forward: func(ctx context.Context, _ *saga.State) error {
				// Per-actuary EmployeeLimit gate. Fires whenever an employee
				// is the acting party — regardless of whether the resulting
				// order's system_type is "employee" (legacy direct), "bank"
				// (Phase 3 employee→bank), or "client" (employee on-behalf).
				// The lookup keys on ActingEmployeeID when set, falling back
				// to UserID for the legacy system_type=="employee" path that
				// pre-dates ActingEmployeeID.
				actingForLimit := req.ActingEmployeeID
				if actingForLimit == 0 && req.SystemType == "employee" {
					actingForLimit = req.UserID
				}
				if actingForLimit != 0 && s.actuaryClient != nil && req.Direction == "buy" {
					info, aerr := s.actuaryClient.GetActuaryLimit(ctx, actingForLimit)
					switch {
					case aerr == nil:
						rsdAmt, cerr := s.convertToRSD(ctx, reserveAmount, reserveCurrency)
						if cerr != nil {
							return cerr
						}
						limitAmountRSD = rsdAmt
						actuaryLimitID = info.ID
						if info.NeedApproval && info.Limit.Sign() > 0 &&
							info.UsedLimit.Add(rsdAmt).GreaterThan(info.Limit) {
							needsApproval = true
						}
					case errors.Is(aerr, stockgrpc.ErrActuaryNotFound):
						// Non-actuary employee — fall through with auto-approve.
					default:
						return status.Errorf(codes.Internal, "actuary limit lookup failed: %v", aerr)
					}
				}

				if actuaryLimitID != 0 {
					amt := limitAmountRSD
					order.LimitAmountRSD = &amt
					aid := actuaryLimitID
					order.LimitActuaryID = &aid
				}

				if needsApproval {
					// Status stays "pending"; supervisor will approve later.
					order.LastModification = time.Now()
				} else {
					order.Status = "approved"
					order.ApprovedBy = approvalActor(req.SystemType)
					order.LastModification = time.Now()
				}
				return s.orderRepo.Update(order)
			},
			// No Backward needed: if this step fails, the saga walks back
			// to reserve_funds/reserve_holding/persist_order_pending and
			// runs their Backwards in reverse.
		})

	if err := sg.Execute(ctx, state); err != nil {
		return nil, err
	}

	if needsApproval {
		// Order sits in "pending" until a supervisor calls ApproveOrder.
		// Publish the order-created event with status=pending.
		if s.producer != nil {
			evt := buildOrderEvent(order)
			go func() { _ = s.producer.PublishOrderCreated(context.Background(), evt) }()
		}
		return order, nil
	}
	StockOrderTotal.WithLabelValues(req.OrderType, "approved").Inc()

	// Bump the actuary's used_limit for auto-approved employee orders. This
	// is best-effort: a failure here doesn't unwind the saga — the order is
	// already approved and funds are already reserved. The order stamps the
	// amount/actuary-id so a human can reconcile if the call failed.
	if actuaryLimitID != 0 && limitAmountRSD.Sign() > 0 {
		if err := s.actuaryClient.IncrementUsedLimit(ctx, actuaryLimitID, limitAmountRSD); err != nil {
			log.Printf("WARN: IncrementUsedLimit on order %d failed: %v", order.ID, err)
		}
	}

	// Publish Kafka event after the saga commits (Phase-2 invariant: no Kafka
	// inside the saga transaction).
	if s.producer != nil {
		evt := buildOrderEvent(order)
		go func() { _ = s.producer.PublishOrderCreated(context.Background(), evt) }()
	}

	return order, nil
}

// convertToRSD returns the RSD-equivalent of `amount` denominated in
// `currency`. Same-currency (RSD) returns the input verbatim without an
// exchange-service call. Used by the actuary limit gate to compare reservation
// cost against the RSD-denominated limit/used_limit.
func (s *OrderService) convertToRSD(ctx context.Context, amount decimal.Decimal, currency string) (decimal.Decimal, error) {
	if amount.Sign() == 0 {
		return decimal.Zero, nil
	}
	if currency == "" || currency == "RSD" {
		return amount, nil
	}
	if s.exchangeClient == nil {
		// No client wired — fall back to the native amount (conservative:
		// under-reports for foreign currencies, but prevents false rejects
		// in bare-bones tests that don't configure exchange).
		return amount, nil
	}
	resp, err := s.exchangeClient.Convert(ctx, &exchangepb.ConvertRequest{
		FromCurrency: currency,
		ToCurrency:   "RSD",
		Amount:       amount.StringFixed(8),
	})
	if err != nil {
		return decimal.Zero, status.Errorf(codes.Internal, "convert to RSD failed: %v", err)
	}
	rsd, err := decimal.NewFromString(resp.ConvertedAmount)
	if err != nil {
		return decimal.Zero, status.Errorf(codes.Internal, "invalid converted amount: %v", err)
	}
	return rsd, nil
}

// ApproveOrder sets an order to "approved" status.
func (s *OrderService) ApproveOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.Status != "pending" {
		return nil, errors.New("order is not pending")
	}

	// Check if settlement date has passed (for futures)
	if s.isSettlementExpired(order) {
		return nil, errors.New("cannot approve: settlement date has passed")
	}

	order.Status = "approved"
	order.ApprovedBy = supervisorName
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}
	StockOrderTotal.WithLabelValues(order.OrderType, "approved").Inc()

	// Bump the actuary's used_limit now that the pending order is approved.
	// Best-effort (warn on error) — the order is already persisted as
	// approved so execution can start regardless.
	if s.actuaryClient != nil && order.LimitActuaryID != nil && order.LimitAmountRSD != nil && order.LimitAmountRSD.Sign() > 0 {
		if err := s.actuaryClient.IncrementUsedLimit(context.Background(), *order.LimitActuaryID, *order.LimitAmountRSD); err != nil {
			log.Printf("WARN: IncrementUsedLimit on approve(order=%d) failed: %v", order.ID, err)
		}
	}

	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderApproved(context.Background(), buildOrderEvent(order)) }()
	}

	return order, nil
}

// DeclineOrder sets an order to "declined" status.
func (s *OrderService) DeclineOrder(orderID uint64, supervisorID uint64, supervisorName string) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.Status != "pending" {
		return nil, errors.New("order is not pending")
	}

	order.Status = "declined"
	order.ApprovedBy = supervisorName
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}
	StockOrderTotal.WithLabelValues(order.OrderType, "declined").Inc()

	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderDeclined(context.Background(), buildOrderEvent(order)) }()
	}

	return order, nil
}

// CancelOrder cancels an unfilled (or partially filled) order for the given
// (user_id, system_type) owner. Cross-system lookups return "order not found"
// without leaking existence.
func (s *OrderService) CancelOrder(orderID, userID uint64, systemType string) (*model.Order, error) {
	order, err := s.orderRepo.GetByIDWithOwner(orderID, userID, systemType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	if order.IsDone {
		return nil, errors.New("order is already completed")
	}
	if order.Status == "declined" || order.Status == "cancelled" {
		return nil, errors.New("order is already declined/cancelled")
	}

	// Capture whether this order ever counted against the actuary's
	// used_limit. Pending orders never incremented (they were stopped at the
	// limit gate); approved orders did (either at placement or supervisor
	// approve). Only approved-and-not-yet-fully-filled orders deserve a
	// partial refund.
	wasApproved := order.Status == "approved"

	order.Status = "cancelled"
	order.IsDone = true
	order.LastModification = time.Now()

	if err := s.orderRepo.Update(order); err != nil {
		return nil, err
	}

	// Release any outstanding funds reservation (buy orders) and holding
	// reservation (sell orders). Both calls are safe no-ops if the
	// reservation was already released or fully settled.
	cancelCtx := context.Background()
	if order.Direction == "buy" && s.accountClient != nil {
		if _, relErr := s.accountClient.ReleaseReservation(cancelCtx, order.ID); relErr != nil {
			log.Printf("WARN: cancel order %d: ReleaseReservation failed: %v", order.ID, relErr)
		}
	}
	if order.Direction == "sell" && s.holdingReservationSvc != nil {
		if _, relErr := s.holdingReservationSvc.Release(cancelCtx, order.ID); relErr != nil {
			log.Printf("WARN: cancel order %d: holding Release failed: %v", order.ID, relErr)
		}
	}

	// Refund the unfilled portion of the actuary's used_limit when an
	// approved order is cancelled. Prorata = remaining / total.
	// Pending-order cancellations never entered the used_limit so nothing to
	// refund there. Best-effort — a gRPC failure here doesn't roll back the
	// cancel because the order is already cancelled and funds already
	// released.
	if wasApproved && s.actuaryClient != nil && order.LimitActuaryID != nil &&
		order.LimitAmountRSD != nil && order.LimitAmountRSD.Sign() > 0 &&
		order.Quantity > 0 {
		unfilledRatio := decimal.NewFromInt(order.RemainingPortions).Div(decimal.NewFromInt(order.Quantity))
		refund := order.LimitAmountRSD.Mul(unfilledRatio)
		if refund.Sign() > 0 {
			if err := s.actuaryClient.DecrementUsedLimit(cancelCtx, *order.LimitActuaryID, refund); err != nil {
				log.Printf("WARN: DecrementUsedLimit on cancel(order=%d) failed: %v", order.ID, err)
			}
		}
	}

	if s.producer != nil {
		go func() { _ = s.producer.PublishOrderCancelled(context.Background(), buildOrderEvent(order)) }()
	}

	return order, nil
}

// GetOrder retrieves an order with (user_id, system_type) ownership check.
// Cross-system lookups return "order not found" without leaking existence.
func (s *OrderService) GetOrder(orderID, userID uint64, systemType string) (*model.Order, []model.OrderTransaction, error) {
	order, err := s.orderRepo.GetByIDWithOwner(orderID, userID, systemType)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("order not found")
		}
		return nil, nil, err
	}

	txns, err := s.txRepo.ListByOrderID(orderID)
	if err != nil {
		return nil, nil, err
	}
	return order, txns, nil
}

// ListMyOrders returns paginated orders for a (user_id, system_type) owner.
func (s *OrderService) ListMyOrders(userID uint64, systemType string, filter repository.OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListByUser(userID, systemType, filter)
}

// ListAllOrders returns paginated orders for supervisor view.
func (s *OrderService) ListAllOrders(filter repository.OrderFilter) ([]model.Order, int64, error) {
	return s.orderRepo.ListAll(filter)
}

// --- Helpers ---

// computeNativeReservation returns the native-currency amount to reserve,
// including slippage buffer (market/stop only) and commission estimate.
func (s *OrderService) computeNativeReservation(req CreateOrderRequest, listing *model.Listing, contractSize int64) (decimal.Decimal, error) {
	qty := decimal.NewFromInt(req.Quantity)
	cs := decimal.NewFromInt(contractSize)
	var unit decimal.Decimal
	switch req.OrderType {
	case "limit", "stop_limit":
		if req.LimitValue == nil {
			return decimal.Zero, status.Error(codes.InvalidArgument, "limit order requires limit_value")
		}
		unit = *req.LimitValue
	case "market", "stop":
		// Worst-case ask — listing.High is already updated during sync.
		unit = listing.High
		if unit.IsZero() {
			// Fallback to Price when High isn't populated (e.g., brand-new
			// listing that hasn't received a daily snapshot yet).
			unit = listing.Price
		}
	default:
		return decimal.Zero, status.Errorf(codes.InvalidArgument, "unknown order_type %q", req.OrderType)
	}
	base := qty.Mul(unit).Mul(cs)
	if req.OrderType == "market" || req.OrderType == "stop" {
		base = base.Mul(decimal.NewFromInt(1).Add(s.settings.MarketSlippagePct()))
	}
	return base.Mul(decimal.NewFromInt(1).Add(s.settings.CommissionRate())), nil
}

// accountCurrency resolves an account's currency_code via account-service.
// Returns an empty string (not an error) when the stub is absent — the caller
// treats that as "same as listing currency" for graceful degradation in tests.
func (s *OrderService) accountCurrency(ctx context.Context, accountID uint64) (string, error) {
	if s.accountClient == nil {
		return "", nil
	}
	stub := s.accountClient.Stub()
	if stub == nil {
		return "", nil
	}
	resp, err := stub.GetAccount(ctx, &accountpb.GetAccountRequest{Id: accountID})
	if err != nil {
		return "", status.Errorf(codes.FailedPrecondition, "account lookup failed: %v", err)
	}
	return resp.GetCurrencyCode(), nil
}

// lookupForexPair fetches the ForexPair referenced by a forex-listing.
func (s *OrderService) lookupForexPair(listing *model.Listing) (*model.ForexPair, error) {
	if s.forexRepo == nil {
		return nil, status.Error(codes.Internal, "forex pair lookup not configured")
	}
	fp, err := s.forexRepo.GetByID(listing.SecurityID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "forex pair lookup failed: %v", err)
	}
	return fp, nil
}

// contractSizeForSecurity returns the default contract size for a security
// type. Callers with access to the futures model (for per-contract sizes)
// override this at the handler layer.
func contractSizeForSecurity(securityType string) int64 {
	switch securityType {
	case "forex":
		return 1000
	default:
		return 1
	}
}

// approvalActor returns the value to store in Order.ApprovedBy at the end of
// the placement saga. Client self-placed orders and bank orders are
// auto-approved with a sentinel string. Employee-on-behalf-of-client
// orders leave this blank so a human approver can be stamped later.
func approvalActor(systemType string) string {
	if systemType == "client" || systemType == "bank" {
		return "no need for approval"
	}
	return ""
}

// defaultOrderSettings is used when NewOrderService is invoked with a nil
// settings provider. The constants are tuned in the file preamble.
type defaultOrderSettings struct{}

func (defaultOrderSettings) CommissionRate() decimal.Decimal {
	return decimal.NewFromFloat(defaultCommissionRate)
}
func (defaultOrderSettings) MarketSlippagePct() decimal.Decimal {
	return decimal.NewFromFloat(defaultMarketSlippagePct)
}

// calculateCommission computes the order commission.
// Market/Stop: min(14% × price, $7)
// Limit/Stop-Limit: min(24% × price, $12)
func calculateCommission(orderType string, approxPrice decimal.Decimal) decimal.Decimal {
	switch orderType {
	case "limit", "stop_limit":
		pct := approxPrice.Mul(decimal.NewFromFloat(0.24))
		cap := decimal.NewFromFloat(12)
		if pct.LessThan(cap) {
			return pct.Round(2)
		}
		return cap
	default: // "market", "stop"
		pct := approxPrice.Mul(decimal.NewFromFloat(0.14))
		cap := decimal.NewFromFloat(7)
		if pct.LessThan(cap) {
			return pct.Round(2)
		}
		return cap
	}
}

func (s *OrderService) isTestingMode() bool {
	if s.settingRepo == nil {
		return false
	}
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

func (s *OrderService) isAfterHours(listing *model.Listing) bool {
	if listing.Exchange.CloseTime == "" {
		return false
	}
	closeH, closeM := parseTimeHM(listing.Exchange.CloseTime)
	offset := parseTimezoneOffsetSafe(listing.Exchange.TimeZone)
	loc := time.FixedZone("ex", offset*3600)
	now := time.Now().In(loc)

	closeMinutes := closeH*60 + closeM
	nowMinutes := now.Hour()*60 + now.Minute()

	return nowMinutes >= closeMinutes && nowMinutes < closeMinutes+240
}

func (s *OrderService) isSettlementExpired(order *model.Order) bool {
	if s.securityRepo == nil {
		return false
	}

	switch order.SecurityType {
	case "futures":
		settlementDate, err := s.securityRepo.GetFuturesSettlementDate(order.ListingID)
		if err != nil {
			return false // if we can't look up, don't block
		}
		return time.Now().After(settlementDate)
	default:
		return false // stocks and forex have no settlement date
	}
}

// buildOrderEvent creates a Kafka event message from an order.
func buildOrderEvent(order *model.Order) map[string]interface{} {
	return map[string]interface{}{
		"order_id":      order.ID,
		"user_id":       order.UserID,
		"direction":     order.Direction,
		"order_type":    order.OrderType,
		"security_type": order.SecurityType,
		"ticker":        order.Ticker,
		"quantity":      order.Quantity,
		"status":        order.Status,
		"timestamp":     time.Now().Unix(),
	}
}

// parseTimeHM parses "09:30" to (9, 30).
func parseTimeHM(t string) (int, int) {
	if len(t) < 5 {
		return 0, 0
	}
	h := int(t[0]-'0')*10 + int(t[1]-'0')
	m := int(t[3]-'0')*10 + int(t[4]-'0')
	return h, m
}

func parseTimezoneOffsetSafe(tz string) int {
	val := 0
	negative := false
	for _, c := range tz {
		if c == '-' {
			negative = true
		} else if c == '+' {
			continue
		} else if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
		}
	}
	if negative {
		val = -val
	}
	return val
}
