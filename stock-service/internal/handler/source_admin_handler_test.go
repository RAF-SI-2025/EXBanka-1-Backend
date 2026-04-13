package handler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/source"
)

// fakeSwitchableSvc is a test double for service.SwitchableSyncService.
type fakeSwitchableSvc struct {
	switchErr  error
	lastSource string
	statusName string
	statusVal  string
	lastErr    string
	startedAt  time.Time
}

func (f *fakeSwitchableSvc) SwitchSource(_ context.Context, s source.Source) error {
	f.lastSource = s.Name()
	return f.switchErr
}

func (f *fakeSwitchableSvc) GetStatus() (string, string, time.Time, string) {
	return f.statusVal, f.lastErr, f.startedAt, f.statusName
}

// fakeSrc satisfies source.Source with all methods returning empty/nil.
type fakeSrc struct{ name string }

func (f *fakeSrc) Name() string { return f.name }

func (f *fakeSrc) FetchExchanges(_ context.Context) ([]model.StockExchange, error) {
	return nil, nil
}

func (f *fakeSrc) FetchStocks(_ context.Context) ([]source.StockWithListing, error) {
	return nil, nil
}

func (f *fakeSrc) FetchFutures(_ context.Context) ([]source.FuturesWithListing, error) {
	return nil, nil
}

func (f *fakeSrc) FetchForex(_ context.Context) ([]source.ForexWithListing, error) {
	return nil, nil
}

func (f *fakeSrc) FetchOptions(_ context.Context, _ *model.Stock) ([]model.Option, error) {
	return nil, nil
}

func (f *fakeSrc) RefreshPrices(_ context.Context) error { return nil }

func buildFactory() handler.SourceFactory {
	return func(name string) (source.Source, error) {
		switch name {
		case "external", "generated", "simulator":
			return &fakeSrc{name: name}, nil
		default:
			return nil, errors.New("unknown")
		}
	}
}

func TestSourceAdminHandler_SwitchSource_Valid(t *testing.T) {
	svc := &fakeSwitchableSvc{statusName: "external", statusVal: "idle"}
	h := handler.NewSourceAdminHandler(svc, buildFactory())
	resp, err := h.SwitchSource(context.Background(), &pb.SwitchSourceRequest{Source: "generated"})
	require.NoError(t, err)
	require.Equal(t, "generated", svc.lastSource)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Status)
}

func TestSourceAdminHandler_SwitchSource_RejectsUnknown(t *testing.T) {
	svc := &fakeSwitchableSvc{}
	h := handler.NewSourceAdminHandler(svc, buildFactory())
	_, err := h.SwitchSource(context.Background(), &pb.SwitchSourceRequest{Source: "garbage"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSourceAdminHandler_GetSourceStatus(t *testing.T) {
	svc := &fakeSwitchableSvc{
		statusName: "simulator",
		statusVal:  "reseeding",
		startedAt:  time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC),
		lastErr:    "",
	}
	h := handler.NewSourceAdminHandler(svc, buildFactory())
	resp, err := h.GetSourceStatus(context.Background(), &pb.GetSourceStatusRequest{})
	require.NoError(t, err)
	require.Equal(t, "simulator", resp.Source)
	require.Equal(t, "reseeding", resp.Status)
	require.NotEmpty(t, resp.StartedAt)
}
