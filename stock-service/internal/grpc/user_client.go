package grpc

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userpb "github.com/exbanka/contract/userpb"
)

// ActuaryLimitInfo is the subset of ActuaryInfo that the order placement
// saga consults when deciding whether to auto-approve or leave the order
// pending for supervisor review.
type ActuaryLimitInfo struct {
	ID           uint64
	EmployeeID   uint64
	Limit        decimal.Decimal
	UsedLimit    decimal.Decimal
	NeedApproval bool
}

// Remaining returns Limit - UsedLimit (never negative).
func (a ActuaryLimitInfo) Remaining() decimal.Decimal {
	r := a.Limit.Sub(a.UsedLimit)
	if r.IsNegative() {
		return decimal.Zero
	}
	return r
}

// ActuaryClient is a thin wrapper around userpb.ActuaryServiceClient used by
// the order placement saga to enforce per-actuary limits. Keeping the
// concrete client out of the service layer makes the service easy to mock in
// tests.
type ActuaryClient struct {
	stub userpb.ActuaryServiceClient
}

// NewActuaryClient builds an ActuaryClient around an existing gRPC stub.
func NewActuaryClient(stub userpb.ActuaryServiceClient) *ActuaryClient {
	return &ActuaryClient{stub: stub}
}

// ErrActuaryNotFound is returned when an employee has no actuary_limits row
// (i.e., they are not an EmployeeAgent / EmployeeSupervisor / EmployeeAdmin).
// Callers treat this as "no limit to enforce".
var ErrActuaryNotFound = errors.New("actuary limit not found")

// GetActuaryLimit looks up the actuary limit for a given employee ID. Returns
// ErrActuaryNotFound when the employee has no actuary row — the caller
// (order placement) treats that as "no limit to enforce" and lets the order
// through without a limit check.
func (c *ActuaryClient) GetActuaryLimit(ctx context.Context, employeeID uint64) (*ActuaryLimitInfo, error) {
	resp, err := c.stub.GetActuaryInfo(ctx, &userpb.GetActuaryInfoRequest{EmployeeId: employeeID})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			// user-service returns NotFound / FailedPrecondition / InvalidArgument
			// for non-actuary employees. Treat all of those as "no limit".
			switch st.Code() {
			case codes.NotFound, codes.FailedPrecondition, codes.InvalidArgument:
				return nil, ErrActuaryNotFound
			}
		}
		return nil, err
	}
	limit, _ := decimal.NewFromString(resp.Limit)
	used, _ := decimal.NewFromString(resp.UsedLimit)
	return &ActuaryLimitInfo{
		ID:           resp.Id,
		EmployeeID:   resp.EmployeeId,
		Limit:        limit,
		UsedLimit:    used,
		NeedApproval: resp.NeedApproval,
	}, nil
}

// IncrementUsedLimit bumps an actuary's used_limit by the given positive RSD
// amount. Used after an order is auto-approved (or after a supervisor
// approves a pending order) so future placements count against the same
// budget window until the daily reset.
func (c *ActuaryClient) IncrementUsedLimit(ctx context.Context, actuaryID uint64, amountRSD decimal.Decimal) error {
	if amountRSD.Sign() <= 0 {
		return errors.New("amount must be > 0")
	}
	_, err := c.stub.UpdateUsedLimit(ctx, &userpb.UpdateUsedLimitRequest{
		Id:       actuaryID,
		Amount:   amountRSD.String(),
		Currency: "RSD",
	})
	return err
}

// DecrementUsedLimit reduces an actuary's used_limit by the given positive
// RSD amount. Called on cancel of an approved order to refund the unfilled
// portion back into the actuary's available budget. The user-service clamps
// the resulting used_limit at zero so double-refunds cannot drive it below
// zero.
func (c *ActuaryClient) DecrementUsedLimit(ctx context.Context, actuaryID uint64, amountRSD decimal.Decimal) error {
	if amountRSD.Sign() <= 0 {
		return errors.New("amount must be > 0")
	}
	_, err := c.stub.UpdateUsedLimit(ctx, &userpb.UpdateUsedLimitRequest{
		Id:       actuaryID,
		Amount:   amountRSD.Neg().String(),
		Currency: "RSD",
	})
	return err
}

// Stub exposes the underlying generated client for call sites that need
// non-wrapped RPCs. Currently unused — kept for symmetry with AccountClient.
func (c *ActuaryClient) Stub() userpb.ActuaryServiceClient {
	return c.stub
}
