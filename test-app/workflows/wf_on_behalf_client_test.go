//go:build integration
// +build integration

package workflows

import (
	"testing"
)

// TestOnBehalfClient_EmployeePlacesOrderForClient is a regression
// scaffold for the on-behalf-of-client trading flow under plan
// docs/superpowers/plans/2026-04-27-owner-type-schema.md.
//
// Flow
//
//	Employee calls POST /api/v1/clients/:id/orders. The api-gateway
//	middleware OwnerFromURLParam("client_id") resolves owner=client,
//	owner_id=<client_id>, acting_employee_id=<employee>. Stock-service
//	creates the order with owner_type=client/owner_id=<client>. The
//	resulting holding (on fill) is owned by the client. The actuary
//	limit (if any) is enforced against acting_employee_id, NOT
//	owner_id.
//
// Expected behaviour
//
//	- POST returns 201; the order is owner_type=client.
//	- The client's account is debited (not the bank's).
//	- The fill produces a holding owned by the client.
//	- Side-effect rows (orders, capital_gains, ledger entries) carry
//	  acting_employee_id=<employee>.
//
// Status
//
//	Scaffold only — exercising the full /clients/:id/orders flow needs
//	a setupActivatedClient helper that pre-funds the client's account,
//	plus assertions on the resulting holding ownership. Both pieces
//	exist in helpers_test.go individually; threading them together
//	is a follow-up.
func TestOnBehalfClient_EmployeePlacesOrderForClient(t *testing.T) {
	t.Parallel()
	t.Skip("requires test setup for on-behalf-of-client flow with pre-funded client account + holding-ownership assertion — implement when the funded-client helper lands in helpers_test.go")
}
