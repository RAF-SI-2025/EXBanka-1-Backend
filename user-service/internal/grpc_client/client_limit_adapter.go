package grpc_client

import (
	"context"

	clientpb "github.com/exbanka/contract/clientpb"
)

// ClientLimitAdapter adapts clientpb.ClientLimitServiceClient to the
// service.ClientLimitClient interface used by BlueprintService.
type ClientLimitAdapter struct {
	client clientpb.ClientLimitServiceClient
}

func NewClientLimitAdapter(client clientpb.ClientLimitServiceClient) *ClientLimitAdapter {
	return &ClientLimitAdapter{client: client}
}

func (a *ClientLimitAdapter) SetClientLimits(ctx context.Context, clientID int64, dailyLimit, monthlyLimit, transferLimit string, setByEmployee int64) error {
	_, err := a.client.SetClientLimits(ctx, &clientpb.SetClientLimitRequest{
		ClientId:      clientID,
		DailyLimit:    dailyLimit,
		MonthlyLimit:  monthlyLimit,
		TransferLimit: transferLimit,
		SetByEmployee: setByEmployee,
	})
	return err
}
