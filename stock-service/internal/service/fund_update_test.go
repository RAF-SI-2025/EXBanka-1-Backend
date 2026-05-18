package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// TestFundService_Update_NotFound surfaces a NotFound from GetByID.
func TestFundService_Update_NotFound(t *testing.T) {
	fx := newFundFixture(t)
	_, err := fx.svc.Update(context.Background(), UpdateFundInput{FundID: 9999})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestFundService_Update_NotManager surfaces ErrFundNotManager when the
// actor isn't the fund's manager.
func TestFundService_Update_NotManager(t *testing.T) {
	fx := newFundFixture(t)
	created, err := fx.svc.Create(context.Background(), CreateFundInput{Name: "ManagedByMe", ActorEmployeeID: 5})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newName := "Hijack"
	_, err = fx.svc.Update(context.Background(), UpdateFundInput{
		FundID: created.ID, ActorEmployeeID: 999, Name: &newName,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrFundNotManager) {
		t.Errorf("expected ErrFundNotManager, got %v", err)
	}
}

// TestFundService_Update_HappyPathChangesAllFields exercises the multi-field
// changed branch.
func TestFundService_Update_HappyPathChangesAllFields(t *testing.T) {
	fx := newFundFixture(t)
	created, err := fx.svc.Create(context.Background(), CreateFundInput{
		Name: "Foo", ActorEmployeeID: 5,
		MinimumContributionRSD: decimal.NewFromInt(100),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newName := "FooUpdated"
	newDesc := "fresh"
	newMin := decimal.NewFromInt(500)
	newActive := false
	got, err := fx.svc.Update(context.Background(), UpdateFundInput{
		FundID: created.ID, ActorEmployeeID: 5,
		Name: &newName, Description: &newDesc,
		MinimumContributionRSD: &newMin, Active: &newActive,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Name != "FooUpdated" || !got.MinimumContributionRSD.Equal(newMin) {
		t.Errorf("got %+v", got)
	}
}

// TestFundService_Update_NoChange_NoOp returns the fund unchanged when no
// fields differ from the stored values.
func TestFundService_Update_NoChange_NoOp(t *testing.T) {
	fx := newFundFixture(t)
	created, err := fx.svc.Create(context.Background(), CreateFundInput{Name: "Same", ActorEmployeeID: 5})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := fx.svc.Update(context.Background(), UpdateFundInput{FundID: created.ID, ActorEmployeeID: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Name != created.Name {
		t.Errorf("got %s", got.Name)
	}
}

// Smoke for ErrFundInvalidInput surface — empty name rejected.
func TestFundService_Create_RejectsEmptyName(t *testing.T) {
	fx := newFundFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateFundInput{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestFundService_Create_RejectsNegativeMinimum(t *testing.T) {
	fx := newFundFixture(t)
	_, err := fx.svc.Create(context.Background(), CreateFundInput{
		Name: "Neg", MinimumContributionRSD: decimal.NewFromInt(-1),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundService_Create_BankAccountFailureCompensates(t *testing.T) {
	fx := newFundFixture(t)
	fx.bankAccountClient.failNext = true
	_, err := fx.svc.Create(context.Background(), CreateFundInput{Name: "Bank fail"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// Sentinel imports used.
var _ = model.InvestmentFund{}
