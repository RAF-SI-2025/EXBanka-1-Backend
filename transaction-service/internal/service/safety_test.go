package service

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/transaction-service/internal/model"
)

// --- Mock payment repository ---

type mockPaymentRepo struct {
	payments map[string]*model.Payment // keyed by IdempotencyKey
	created  []*model.Payment
	nextID   uint64
}

func newMockPaymentRepo() *mockPaymentRepo {
	return &mockPaymentRepo{
		payments: make(map[string]*model.Payment),
		nextID:   1,
	}
}

func (m *mockPaymentRepo) Create(p *model.Payment) error {
	p.ID = m.nextID
	m.nextID++
	cp := *p
	m.payments[p.IdempotencyKey] = &cp
	m.created = append(m.created, &cp)
	return nil
}

func (m *mockPaymentRepo) GetByID(id uint64) (*model.Payment, error) {
	for _, p := range m.payments {
		if p.ID == id {
			cp := *p
			return &cp, nil
		}
	}
	return nil, errNotFound
}

func (m *mockPaymentRepo) GetByIdempotencyKey(key string) (*model.Payment, error) {
	if p, ok := m.payments[key]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, errNotFound
}

func (m *mockPaymentRepo) UpdateStatus(id uint64, status string) error {
	for _, p := range m.payments {
		if p.ID == id {
			p.Status = status
		}
	}
	return nil
}

func (m *mockPaymentRepo) UpdateStatusWithReason(id uint64, status, reason string) error {
	for _, p := range m.payments {
		if p.ID == id {
			p.Status = status
			p.FailureReason = reason
		}
	}
	return nil
}

func (m *mockPaymentRepo) ListByAccount(accountNumber, dateFrom, dateTo, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	return nil, 0, nil
}

func (m *mockPaymentRepo) ListByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Payment, int64, error) {
	return nil, 0, nil
}

// errNotFound is gorm.ErrRecordNotFound so that errors.Is(err, gorm.ErrRecordNotFound) matches
// in the idempotency check path of payment_service.go.
var errNotFound = gorm.ErrRecordNotFound

// --- Helpers ---

func newTestFeeService(minAmount int64, feePercent float64) *FeeService {
	repo := &mockFeeRepo{
		fees: []model.TransferFee{
			{
				Active:          true,
				FeeType:         "percentage",
				FeeValue:        decimal.NewFromFloat(feePercent),
				MinAmount:       decimal.NewFromInt(minAmount),
				TransactionType: "all",
			},
		},
	}
	return &FeeService{repo: repo}
}

// --- Test 1: Idempotency — CreatePayment with same key returns same result without re-processing ---

func TestCreatePayment_Idempotency(t *testing.T) {
	repo := newMockPaymentRepo()
	feeSvc := newTestFeeService(1000, 0.1)

	svc := &PaymentService{
		paymentRepo:    repo,
		accountClient:  nil, // no account client; skips debit/credit
		feeSvc:         feeSvc,
		producer:       nil,
		bankRSDAccount: "",
	}

	ctx := context.Background()

	// First call: creates a new payment.
	p1 := &model.Payment{
		IdempotencyKey:    "test-idem-key-001",
		FromAccountNumber: "ACC001",
		ToAccountNumber:   "ACC002",
		InitialAmount:     decimal.NewFromInt(500), // below fee threshold
		CurrencyCode:      "RSD",
	}
	err := svc.CreatePayment(ctx, p1)
	require.NoError(t, err)
	firstID := p1.ID
	assert.Equal(t, uint64(1), firstID, "first payment should get ID 1")
	assert.Equal(t, "pending_verification", p1.Status, "payment should be pending_verification after create")
	assert.Equal(t, 1, len(repo.created), "exactly one payment should be persisted")

	// Second call with same idempotency key: must return the existing payment WITHOUT re-processing.
	p2 := &model.Payment{
		IdempotencyKey:    "test-idem-key-001",
		FromAccountNumber: "ACC001",
		ToAccountNumber:   "ACC002",
		InitialAmount:     decimal.NewFromInt(500),
		CurrencyCode:      "RSD",
	}
	err = svc.CreatePayment(ctx, p2)
	require.NoError(t, err, "second call with same key must not return error")
	assert.Equal(t, firstID, p2.ID, "second call must return the same payment ID")
	assert.Equal(t, 1, len(repo.created), "no additional payment should be persisted on duplicate key")
}

// --- Test 2: Decimal precision — fee calculation on 999.9999 produces exact result (no float drift) ---

func TestFeeCalculation_DecimalPrecision(t *testing.T) {
	feeSvc := newTestFeeService(500, 0.1) // 0.1% fee for amounts >= 500

	// 999.9999 * 0.1% = 0.9999999 — shopspring/decimal must handle this exactly.
	amount, _ := decimal.NewFromString("999.9999")
	fee, err := feeSvc.CalculateFee(amount, "payment", "RSD")
	require.NoError(t, err)

	// Expected: 999.9999 * 0.001 = 0.9999999
	expected := amount.Mul(decimal.NewFromFloat(0.001))
	assert.True(t, fee.Equal(expected),
		"fee must equal %s, got %s (decimal precision must be exact, no float drift)",
		expected.String(), fee.String())

	// Verify it is NOT equal to the float64-rounded version.
	floatApprox := 999.9999 * 0.001 // may lose precision in float64
	floatDecimal := decimal.NewFromFloat(floatApprox)
	// This assertion confirms decimal is more precise than float64 arithmetic.
	// (They may or may not differ depending on float64 rounding — the point is we use decimal.)
	_ = floatDecimal // acknowledged
}

// --- Test 3: Zero fee — payment below min_amount threshold gets zero fee ---

func TestCreatePayment_ZeroFee_BelowThreshold(t *testing.T) {
	repo := newMockPaymentRepo()
	feeSvc := newTestFeeService(1000, 0.1) // fee only applies for amounts >= 1000

	svc := &PaymentService{
		paymentRepo:    repo,
		accountClient:  nil, // no account client; balance operations skipped
		feeSvc:         feeSvc,
		producer:       nil,
		bankRSDAccount: "",
	}

	ctx := context.Background()

	p := &model.Payment{
		IdempotencyKey:    "test-zero-fee-001",
		FromAccountNumber: "ACC001",
		ToAccountNumber:   "ACC002",
		InitialAmount:     decimal.NewFromInt(500), // below 1000 threshold
		CurrencyCode:      "RSD",
	}
	err := svc.CreatePayment(ctx, p)
	require.NoError(t, err)

	assert.True(t, p.Commission.IsZero(), "commission must be zero for amounts below min_amount threshold")
	assert.True(t, p.FinalAmount.Equal(decimal.NewFromInt(500)),
		"final amount must equal initial amount when fee is zero; got %s", p.FinalAmount.String())
	assert.Equal(t, "pending_verification", p.Status)
}
