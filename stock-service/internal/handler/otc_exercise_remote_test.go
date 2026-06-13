package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// fakeCrossBankExerciser records the cross-bank exercise dispatch the REMOTE
// branch of the unified ExerciseContract performs, so tests can assert WHETHER
// a dispatch happened and with WHICH contract + buyer account (SP-2b Task 5).
type fakeCrossBankExerciser struct {
	called bool
	gotReq *stockpb.InitiateOptionExerciseRequest
	resp   *stockpb.InitiateOptionExerciseResponse
	err    error
}

func (f *fakeCrossBankExerciser) InitiateOptionExercise(_ context.Context, req *stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error) {
	f.called = true
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &stockpb.InitiateOptionExerciseResponse{TransactionId: "tx-cb", Status: "pending"}, nil
}

// newRemoteExerciseFixture wires the unified-contract fixture PLUS a fake
// cross-bank exerciser so the REMOTE branch of ExerciseContract is live. The
// account client backs the SP-3 Task 5 strike-account re-assert; by default it
// returns a client-owned USD account matching the buyer client-7 the seeded
// contracts use, so the existing client-holder cases dispatch unchanged. Tests
// that need a different account (bank-owned, mismatched owner/currency) call
// newRemoteExerciseFixtureAcct.
func newRemoteExerciseFixture(t *testing.T, ownRouting int64) (*OTCOptionsHandler, *otcOptionsHandlerFixture, *fakeCrossBankExerciser) {
	t.Helper()
	return newRemoteExerciseFixtureAcct(t, ownRouting, &fakeOTCAccountClient{acct: usdAccount(7)})
}

// newRemoteExerciseFixtureAcct is newRemoteExerciseFixture with an explicit
// account client for the strike-account re-assert (SP-3 Task 5).
func newRemoteExerciseFixtureAcct(t *testing.T, ownRouting int64, accounts OTCAccountClient) (*OTCOptionsHandler, *otcOptionsHandlerFixture, *fakeCrossBankExerciser) {
	t.Helper()
	h, fx := newUnifiedContractFixture(t, ownRouting, "111")
	cb := &fakeCrossBankExerciser{}
	// Wire the account client the same way main.go does (via WithPeerOTCDispatch,
	// which sets h.accounts), then add the exerciser. nil dispatcher/writer are
	// fine: the exercise path only reads h.accounts.
	h = h.WithPeerOTCDispatch(nil, nil, accounts).WithCrossBankExerciser(cb)
	return h, fx, cb
}

// A holder caller (the buyer this bank hosts on a CREDIT remote contract)
// triggers the cross-bank SI-TX exercise dispatch with the right contract id +
// buyer settlement account.
func TestExerciseContract_Remote_HolderDispatches(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 901,
		CrossbankTxID:      "tx-901",
		PostingIndex:       0,
		NegotiationID:      "neg-901",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7", // WE host the buyer
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	resp, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         901,
		ActorUserId:        7, // the buyer/holder
		ActorSystemType:    "client",
		BuyerAccountNumber: "265-12-13",
	})
	if err != nil {
		t.Fatalf("holder exercise: unexpected err: %v", err)
	}
	if !cb.called {
		t.Fatalf("cross-bank exercise was NOT dispatched for a holder caller")
	}
	if cb.gotReq.GetPeerOptionContractId() != 901 {
		t.Errorf("dispatched contract id = %d, want 901", cb.gotReq.GetPeerOptionContractId())
	}
	if cb.gotReq.GetBuyerAccountNumber() != "265-12-13" {
		t.Errorf("dispatched buyer account = %q, want 265-12-13", cb.gotReq.GetBuyerAccountNumber())
	}
	// The dispatch result is projected onto the unified ExerciseResponse: the
	// cross-bank transaction id rides in SagaId, with the strike/shares figures
	// derived from the persisted remote row.
	if resp.GetContractId() != 901 {
		t.Errorf("resp contract id = %d, want 901", resp.GetContractId())
	}
	if resp.GetSagaId() != "tx-cb" {
		t.Errorf("resp saga_id = %q, want tx-cb (cross-bank transaction id)", resp.GetSagaId())
	}
	if resp.GetStatus() != "pending" {
		t.Errorf("resp status = %q, want pending", resp.GetStatus())
	}
	if resp.GetSharesTransferred() != "5" {
		t.Errorf("shares = %q, want 5", resp.GetSharesTransferred())
	}
	if resp.GetStrikeAmountSellerCcy() != "1000" {
		t.Errorf("strike = %q, want 1000 (5 * 200)", resp.GetStrikeAmountSellerCcy())
	}
}

// SP-3 Task 5: the BANK is the buyer/holder of a cross-bank CREDIT contract
// (RemoteBuyerID "employee-<N>"); a caller acting AS THE BANK (actor_system_type
// "bank", on_behalf_of_client_id 0) exercises it. Authorized → the cross-bank
// dispatch fires with the bank's bound settlement account.
func TestExerciseContract_Remote_BankHolderDispatches(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// Bank buyer → the strike-account re-assert requires a BANK account.
	h, fx, cb := newRemoteExerciseFixtureAcct(t, ownRouting, &fakeOTCAccountClient{acct: usdBankAccount()})
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 910,
		CrossbankTxID:      "tx-910",
		PostingIndex:       0,
		NegotiationID:      "neg-910",
		BuyerRoutingNumber: ownRouting, BuyerID: "employee-42", // WE host the BANK buyer
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	resp, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         910,
		ActorUserId:        0,      // bank actor has no user id
		ActorSystemType:    "bank", // acting AS THE BANK
		OnBehalfOfClientId: 0,      // 0 = acting as the bank, not on behalf of a client
		BuyerAccountNumber: "111-BANK-USD-01",
	})
	if err != nil {
		t.Fatalf("bank holder exercise: unexpected err: %v", err)
	}
	if !cb.called {
		t.Fatalf("cross-bank exercise was NOT dispatched for the bank holder")
	}
	if cb.gotReq.GetPeerOptionContractId() != 910 {
		t.Errorf("dispatched contract id = %d, want 910", cb.gotReq.GetPeerOptionContractId())
	}
	// Settlement uses the bank's bound account (gateway-validated as a bank account).
	if cb.gotReq.GetBuyerAccountNumber() != "111-BANK-USD-01" {
		t.Errorf("dispatched buyer account = %q, want 111-BANK-USD-01", cb.gotReq.GetBuyerAccountNumber())
	}
	if resp.GetContractId() != 910 {
		t.Errorf("resp contract id = %d, want 910", resp.GetContractId())
	}
}

// A client caller may NOT exercise a BANK-hosted (employee-<N>) buyer contract,
// even if their client id collides with the employee number → NotFound, no
// dispatch.
func TestExerciseContract_Remote_ClientCannotExerciseBankContract(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 911,
		CrossbankTxID:      "tx-911",
		PostingIndex:       0,
		NegotiationID:      "neg-911",
		BuyerRoutingNumber: ownRouting, BuyerID: "employee-42", // BANK buyer
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         911,
		ActorUserId:        42, // a client whose id collides with the employee number
		ActorSystemType:    "client",
		BuyerAccountNumber: "265-12-13",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("client on a bank contract: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch for a client on a bank-buyer contract")
	}
}

// An employee acting ON BEHALF OF A CLIENT (on_behalf_of_client_id != 0) is NOT
// acting as the bank → may not exercise a bank-hosted contract → NotFound.
func TestExerciseContract_Remote_OnBehalfOfClientNotBank(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 912,
		CrossbankTxID:      "tx-912",
		PostingIndex:       0,
		NegotiationID:      "neg-912",
		BuyerRoutingNumber: ownRouting, BuyerID: "employee-42",
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         912,
		ActorUserId:        0,
		ActorSystemType:    "bank",
		OnBehalfOfClientId: 77, // acting on behalf of a client, NOT as the bank
		BuyerAccountNumber: "111-BANK-USD-01",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("on-behalf-of-client on a bank contract: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch when acting on behalf of a client")
	}
}

// A NON-holder caller (a different client, not the buyer this bank hosts) →
// NotFound, and NO cross-bank dispatch (existence must not leak, money must not
// move).
func TestExerciseContract_Remote_NonHolderNotFound(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 902,
		CrossbankTxID:      "tx-902",
		PostingIndex:       0,
		NegotiationID:      "neg-902",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7", // WE host buyer client-7
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	// Caller is client-5, NOT the buyer (client-7).
	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         902,
		ActorUserId:        5,
		ActorSystemType:    "client",
		BuyerAccountNumber: "265-12-13",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("non-holder: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch for a non-holder (existence/money leak)")
	}
}

// The WRITER/SELLER side (a DEBIT remote contract this bank hosts the SELLER
// of) may NOT exercise — only the buyer/holder can. → NotFound, no dispatch.
func TestExerciseContract_Remote_WriterCannotExercise(t *testing.T) {
	const ownRouting int64 = 111
	const peerBuyerRouting int64 = 333
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	// DEBIT row → this bank holds the SELLER side (client-7 is OUR seller).
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 903,
		CrossbankTxID:      "tx-903",
		PostingIndex:       0,
		NegotiationID:      "neg-903",
		BuyerRoutingNumber: peerBuyerRouting, BuyerID: "client-9",
		SellerRoutingNumber: ownRouting, SellerID: "client-7", // WE host the seller/writer
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "DEBIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	// The local seller/writer (client-7) tries to exercise.
	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         903,
		ActorUserId:        7,
		ActorSystemType:    "client",
		BuyerAccountNumber: "265-12-13",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("writer/seller: expected NotFound (only the buyer may exercise), got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch for the writer/seller side")
	}
}

// An employee caller has no cross-bank client identity → never the holder of a
// remote contract → NotFound, no dispatch.
func TestExerciseContract_Remote_EmployeeNotHolder(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 904,
		CrossbankTxID:      "tx-904",
		PostingIndex:       0,
		NegotiationID:      "neg-904",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7",
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         904,
		ActorUserId:        7,
		ActorSystemType:    "employee", // no cross-bank participant identity
		BuyerAccountNumber: "265-12-13",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("employee: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch for an employee (no cross-bank identity)")
	}
}

// A holder caller on a remote contract who omits the settlement account →
// InvalidArgument (the strike-paying account is required cross-bank); no
// dispatch.
func TestExerciseContract_Remote_MissingBuyerAccount(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 905,
		CrossbankTxID:      "tx-905",
		PostingIndex:       0,
		NegotiationID:      "neg-905",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7",
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:      905,
		ActorUserId:     7,
		ActorSystemType: "client",
		// BuyerAccountNumber omitted
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing buyer account: expected InvalidArgument, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch without a settlement account")
	}
}

// A cross-bank dispatch business rejection (e.g. insufficient funds →
// FailedPrecondition) propagates unmasked from the exerciser.
func TestExerciseContract_Remote_DispatchErrorPropagates(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	cb.err = status.Error(codes.FailedPrecondition, "insufficient funds")
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 906,
		CrossbankTxID:      "tx-906",
		PostingIndex:       0,
		NegotiationID:      "neg-906",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7",
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         906,
		ActorUserId:        7,
		ActorSystemType:    "client",
		BuyerAccountNumber: "265-12-13",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("dispatch error: expected FailedPrecondition (unmasked), got %v", err)
	}
	if !cb.called {
		t.Fatalf("dispatch should have been attempted")
	}
}

// A LOCAL contract (routing == own) routes to the existing local exercise saga
// — NEVER the cross-bank dispatch — even with the cross-bank exerciser wired.
// (The fixture wires no saga deps, so the local path errors out; the load is
// that the cross-bank exerciser was NOT touched.)
func TestExerciseContract_Local_DoesNotDispatchCrossBank(t *testing.T) {
	const ownRouting int64 = 111
	h, fx, cb := newRemoteExerciseFixture(t, ownRouting)
	buyer := uint64(7)
	c := seedLocalContract(t, fx, &buyer, nil) // seller = bank

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:      c.ID,
		ActorUserId:     7, // the local buyer
		ActorSystemType: "client",
	})
	// The local saga path runs (and errors here because deps are unwired); the
	// point is that the cross-bank exerciser is never engaged for a local row.
	if err == nil {
		t.Fatalf("expected the local saga path to run (and error on unwired deps), got nil")
	}
	if cb.called {
		t.Fatalf("a LOCAL contract must NOT trigger the cross-bank dispatch")
	}
}

// A genuinely-missing id (neither local nor remote) is NOT a cross-bank
// exercise: the remote branch leaves it alone (no dispatch) and the request
// falls through to the local exercise path, which surfaces its own error (here
// the fixture wires no saga deps, so it's a deps-not-wired error rather than a
// cross-bank dispatch). The load is critical: no money moves and no peer is
// contacted for a non-existent contract.
func TestExerciseContract_Remote_MissingIdFallsThrough(t *testing.T) {
	const ownRouting int64 = 111
	h, _, cb := newRemoteExerciseFixture(t, ownRouting)

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:      4242,
		ActorUserId:     7,
		ActorSystemType: "client",
	})
	if err == nil {
		t.Fatalf("missing id: expected an error from the local fall-through, got nil")
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch for a non-existent contract")
	}
}

// SP-3 Task 5 security re-assert: a BANK-buyer contract whose caller (acting as
// the bank) binds a NON-bank (client-owned) strike account must be REJECTED at
// the stock-service defense-in-depth layer — NotFound, no dispatch. Without
// this, a bank exercise could pay the strike obligation from a client's account
// of the matching currency. This is the gateway-bypass guard.
func TestExerciseContract_Remote_BankBindingNonBankAccountRejected(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// The bound strike account is a CLIENT account (owner 7), NOT a bank account.
	h, fx, cb := newRemoteExerciseFixtureAcct(t, ownRouting, &fakeOTCAccountClient{acct: usdAccount(7)})
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 920,
		CrossbankTxID:      "tx-920",
		PostingIndex:       0,
		NegotiationID:      "neg-920",
		BuyerRoutingNumber: ownRouting, BuyerID: "employee-42", // BANK buyer
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         920,
		ActorUserId:        0,
		ActorSystemType:    "bank",
		OnBehalfOfClientId: 0,
		BuyerAccountNumber: "111-0000000001-22", // a client's account, matching currency
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("bank binding a client account: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("THEFT VECTOR: cross-bank exercise dispatched the strike against a NON-bank account")
	}
}

// SP-3 Task 5 security re-assert: a CLIENT-buyer contract whose caller binds
// ANOTHER client's account must be REJECTED at the stock-service layer —
// NotFound, no dispatch (belt-and-suspenders to the gateway gate). The bound
// account owner (99) is NOT the buyer client (7).
func TestExerciseContract_Remote_ClientBindingOtherClientAccountRejected(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// The bound account is owned by client 99, but the contract buyer is client-7.
	h, fx, cb := newRemoteExerciseFixtureAcct(t, ownRouting, &fakeOTCAccountClient{acct: usdAccount(99)})
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 921,
		CrossbankTxID:      "tx-921",
		PostingIndex:       0,
		NegotiationID:      "neg-921",
		BuyerRoutingNumber: ownRouting, BuyerID: "client-7", // CLIENT buyer (we host)
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD",
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         921,
		ActorUserId:        7, // the legit buyer principal...
		ActorSystemType:    "client",
		BuyerAccountNumber: "111-0000000001-22", // ...but binding client-99's account
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("client binding another client's account: expected NotFound, got %v", err)
	}
	if cb.called {
		t.Fatalf("THEFT VECTOR: cross-bank exercise dispatched the strike against another client's account")
	}
}

// SP-3 Task 5 security re-assert: a buyer (here the bank) binding a bank account
// of a MISMATCHED currency must be REJECTED (cross-bank SI-TX has no FX) —
// InvalidArgument, no dispatch.
func TestExerciseContract_Remote_StrikeCurrencyMismatchRejected(t *testing.T) {
	const ownRouting int64 = 111
	const peerSellerRouting int64 = 222
	// Bank account, but EUR while the contract strike is USD.
	eurBank := &accountpb.AccountResponse{
		Id:            5002,
		OwnerId:       1_000_000_000,
		AccountNumber: "111-BANK-EUR-01",
		CurrencyCode:  "EUR",
		Status:        "active",
		AccountKind:   "bank",
	}
	h, fx, cb := newRemoteExerciseFixtureAcct(t, ownRouting, &fakeOTCAccountClient{acct: eurBank})
	seedPeerContract(t, fx, &peerContractSeed{
		ID:                 922,
		CrossbankTxID:      "tx-922",
		PostingIndex:       0,
		NegotiationID:      "neg-922",
		BuyerRoutingNumber: ownRouting, BuyerID: "employee-42",
		SellerRoutingNumber: peerSellerRouting, SellerID: "client-3",
		Ticker:         "ACME",
		Quantity:       5,
		StrikePrice:    decimal.NewFromInt(200),
		Currency:       "USD", // contract strike is USD
		SettlementDate: "2030-01-01",
		Direction:      "CREDIT",
		Status:         "active",
		CreatedAt:      time.Now(),
	})

	_, err := h.ExerciseContract(context.Background(), &stockpb.ExerciseContractRequest{
		ContractId:         922,
		ActorUserId:        0,
		ActorSystemType:    "bank",
		OnBehalfOfClientId: 0,
		BuyerAccountNumber: "111-BANK-EUR-01",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("currency mismatch: expected InvalidArgument, got %v", err)
	}
	if cb.called {
		t.Fatalf("cross-bank exercise must NOT dispatch on a currency mismatch")
	}
}
