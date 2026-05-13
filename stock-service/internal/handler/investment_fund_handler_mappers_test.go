package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

func TestToFundResponse(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f := &model.InvestmentFund{
		ID:                     7,
		Name:                   "Foo Fund",
		Description:            "desc",
		ManagerEmployeeID:      42,
		MinimumContributionRSD: decimal.NewFromInt(1000),
		RSDAccountID:           99,
		Active:                 true,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	resp := toFundResponse(f)
	if resp.Id != 7 || resp.Name != "Foo Fund" || resp.Description != "desc" {
		t.Errorf("basic fields wrong: %+v", resp)
	}
	if resp.ManagerEmployeeId != 42 {
		t.Errorf("manager_employee_id: %d", resp.ManagerEmployeeId)
	}
	if resp.MinimumContributionRsd != "1000" {
		t.Errorf("min: %q", resp.MinimumContributionRsd)
	}
	if resp.RsdAccountId != 99 {
		t.Errorf("acct: %d", resp.RsdAccountId)
	}
	if !resp.Active {
		t.Error("active not propagated")
	}
	if resp.CreatedAt == "" || resp.UpdatedAt == "" {
		t.Error("timestamps blank")
	}
}

func TestToContribResponse_NoFxRate(t *testing.T) {
	c := &model.FundContribution{
		ID:             5,
		FundID:         1,
		Direction:      "invest",
		AmountNative:   decimal.NewFromInt(100),
		NativeCurrency: "RSD",
		AmountRSD:      decimal.NewFromInt(100),
		FeeRSD:         decimal.NewFromInt(1),
		Status:         "completed",
	}
	r := toContribResponse(c)
	if r.Id != 5 || r.FundId != 1 {
		t.Errorf("ids: %+v", r)
	}
	if r.FxRate != "" {
		t.Errorf("expected empty fx_rate, got %q", r.FxRate)
	}
	if r.AmountNative != "100" || r.AmountRsd != "100" || r.FeeRsd != "1" {
		t.Errorf("amounts: %+v", r)
	}
}

func TestToContribResponse_WithFxRate(t *testing.T) {
	rate := decimal.NewFromFloat(117.55)
	c := &model.FundContribution{FxRate: &rate}
	r := toContribResponse(c)
	if r.FxRate != "117.55" {
		t.Errorf("fx: %q", r.FxRate)
	}
}

func TestMapFundErr_Nil(t *testing.T) {
	if err := mapFundErr(nil); err != nil {
		t.Errorf("got %v want nil", err)
	}
}

func TestMapFundErr_GormNotFound(t *testing.T) {
	err := mapFundErr(gorm.ErrRecordNotFound)
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestMapFundErr_OtherPasses(t *testing.T) {
	sentinel := errors.New("boom")
	got := mapFundErr(sentinel)
	if !errors.Is(got, sentinel) {
		t.Errorf("expected sentinel passed through, got %v", got)
	}
}

func TestPositionDTOsToProto_Empty(t *testing.T) {
	got := positionDTOsToProto(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestPositionDTOsToProto_Multi(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	rows := []service.PositionDTO{
		{
			FundID:          1,
			FundName:        "Fund A",
			ManagerFullName: "Alice",
			ContributionRSD: decimal.NewFromInt(1000),
			PercentageFund:  decimal.NewFromFloat(0.25),
			CurrentValueRSD: decimal.NewFromInt(1100),
			ProfitRSD:       decimal.NewFromInt(100),
			LastChangedAt:   now,
		},
		{
			FundID:   2,
			FundName: "Fund B",
		},
	}
	got := positionDTOsToProto(rows)
	if len(got) != 2 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].FundId != 1 || got[0].FundName != "Fund A" || got[0].ManagerFullName != "Alice" {
		t.Errorf("row[0] wrong: %+v", got[0])
	}
	if got[0].ContributionRsd != "1000" || got[0].PercentageFund != "0.25" {
		t.Errorf("row[0] decimals: %+v", got[0])
	}
}
