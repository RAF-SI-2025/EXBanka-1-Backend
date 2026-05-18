# Celina 5 SI-TX Refactor — Phase 4 OTC Peer Endpoints Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the 7 SI-TX cross-bank OTC peer endpoints (`GET /api/v3/public-stock`, `POST/PUT/GET/DELETE /api/v3/negotiations/{rid}/{id}`, `GET /api/v3/negotiations/{rid}/{id}/accept`, `GET /api/v3/user/{rid}/{id}`). Acceptance composes a 4-posting `Transaction` (premium money + 1× option-asset both directions) and routes it through the Phase 3 SI-TX TX flow.

**Architecture:** OTC peer routes live on api-gateway, gated by `PeerAuth`. They dispatch via gRPC to a new `PeerOTCService` on stock-service (negotiations + public-stock) and to user-service / client-service for `/user/{rid}/{id}`. Receiver-side persistence of inbound negotiations uses a new `peer_otc_negotiations` table. Acceptance triggers `PeerTxService.InitiateOutboundTxWithPostings` (a new RPC that takes a pre-composed `Transaction` so OTC can reuse the Phase 3 outbound flow without forcing the simple-transfer 2-posting shape).

**Tech Stack:** Same as Phase 3 — gRPC + GORM (glebarez/sqlite for tests) + gin + the existing PeerHTTPClient + OutboundReplayCron.

**End state:**
- 7 new public peer-facing routes return 200/201/204 per SI-TX semantics.
- `peer_otc_negotiations` auto-migrated on stock-service start.
- Acceptance composes the 4-posting envelope and dispatches via PeerTxService.
- `make build / test / lint` all green.

---

## File structure

**New:**
| Path | Responsibility |
|---|---|
| `contract/sitx/otc_types.go` | OTC wire types: `OtcOffer`, `OtcNegotiation`, `OptionDescription`, `UserInformation`, `PublicStocksResponse`, `ForeignBankId`. |
| `contract/sitx/otc_types_test.go` | JSON round-trip tests. |
| `contract/proto/stock/stock.proto` (modify) | `service PeerOTCService` (6 RPCs) + supporting messages. |
| `stock-service/internal/model/peer_otc_negotiation.go` | `PeerOtcNegotiation` GORM model. |
| `stock-service/internal/repository/peer_otc_negotiation_repository.go` | CRUD. |
| `stock-service/internal/repository/peer_otc_negotiation_repository_test.go` | Repository tests. |
| `stock-service/internal/handler/peer_otc_grpc_handler.go` | `PeerOTCService` gRPC server. |
| `stock-service/internal/handler/peer_otc_grpc_handler_test.go` | Handler tests. |
| `api-gateway/internal/grpc/peer_otc_client.go` | gRPC client for PeerOTCService. |
| `api-gateway/internal/handler/peer_otc_handler.go` | 6 OTC routes (5 negotiation + 1 public-stock). |
| `api-gateway/internal/handler/peer_otc_handler_test.go` | Handler tests. |
| `api-gateway/internal/handler/peer_user_handler.go` | 1 user info route. |
| `api-gateway/internal/handler/peer_user_handler_test.go` | Handler tests. |

**Modified:**
| Path | What |
|---|---|
| `contract/proto/transaction/transaction.proto` | Add `InitiateOutboundTxWithPostings` RPC + request message to `PeerTxService` (so OTC accept can pass pre-composed postings). |
| `transaction-service/internal/handler/peer_tx_grpc_handler.go` | Implement `InitiateOutboundTxWithPostings`. |
| `transaction-service/internal/handler/peer_tx_grpc_handler_test.go` | Test the new RPC. |
| `stock-service/cmd/main.go` | AutoMigrate `PeerOtcNegotiation`, construct repo + handler, register `PeerOTCService`. |
| `api-gateway/internal/grpc/peer_otc_client.go` | (new) |
| `api-gateway/internal/router/handlers.go` | Add `PeerOTC` + `PeerUser` to Handlers/Deps. |
| `api-gateway/internal/router/router_v3.go` | Register the 7 new peer routes under `peer` group with PeerAuthMW. |
| `api-gateway/cmd/main.go` | Wire `peerOTCClient` from stock-service. |
| `docs/api/REST_API_v3.md` | Add peer OTC + user-info section under Peer-Bank Protocol. |

---

## Tasks

### Task 1 — SI-TX OTC wire types

**Files:**
- Create: `contract/sitx/otc_types.go`
- Create: `contract/sitx/otc_types_test.go`

- [ ] **Step 1.1: Test — `contract/sitx/otc_types_test.go`**

```go
package sitx_test

import (
	"encoding/json"
	"testing"

	"github.com/exbanka/contract/sitx"
	"github.com/shopspring/decimal"
)

func TestOtcOffer_RoundTrip(t *testing.T) {
	in := sitx.OtcOffer{
		Ticker:         "AAPL",
		Amount:         100,
		PricePerStock:  decimal.NewFromFloat(180.50),
		Currency:       "USD",
		Premium:        decimal.NewFromFloat(700),
		PremiumCurrency: "USD",
		SettlementDate: "2026-12-31",
		LastModifiedBy: sitx.ForeignBankId{RoutingNumber: 222, ID: "user-1"},
	}
	raw, _ := json.Marshal(in)
	var out sitx.OtcOffer
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Ticker != "AAPL" || out.Amount != 100 || !out.PricePerStock.Equal(decimal.NewFromFloat(180.50)) {
		t.Errorf("got %+v", out)
	}
	if out.LastModifiedBy.RoutingNumber != 222 {
		t.Errorf("foreignBankId routing: %d", out.LastModifiedBy.RoutingNumber)
	}
}

func TestOptionDescription_RoundTrip(t *testing.T) {
	in := sitx.OptionDescription{
		Ticker:         "AAPL",
		Amount:         50,
		StrikePrice:    decimal.NewFromFloat(200),
		Currency:       "USD",
		SettlementDate: "2026-12-31",
		NegotiationID:  sitx.ForeignBankId{RoutingNumber: 222, ID: "neg-7"},
	}
	raw, _ := json.Marshal(in)
	var out sitx.OptionDescription
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.NegotiationID.ID != "neg-7" {
		t.Errorf("got %+v", out)
	}
}

func TestUserInformation_RoundTrip(t *testing.T) {
	in := sitx.UserInformation{
		ID:        sitx.ForeignBankId{RoutingNumber: 222, ID: "u1"},
		FirstName: "Marko",
		LastName:  "Marković",
	}
	raw, _ := json.Marshal(in)
	var out sitx.UserInformation
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.FirstName != "Marko" || out.ID.ID != "u1" {
		t.Errorf("got %+v", out)
	}
}
```

- [ ] **Step 1.2: Implement — `contract/sitx/otc_types.go`**

```go
package sitx

import "github.com/shopspring/decimal"

// ForeignBankId is the (routingNumber, id) tuple used by SI-TX wherever a
// resource needs to be identified across banks (negotiations, users,
// public-stock entries). The routingNumber locates the owning bank; the
// id is the bank-local identifier (string for flexibility).
type ForeignBankId struct {
	RoutingNumber int64  `json:"routingNumber"`
	ID            string `json:"id"`
}

// OtcOffer is the in-flight SI-TX offer body. Sent on POST /negotiations
// (initial offer) and PUT /negotiations/{rid}/{id} (counter-offers).
type OtcOffer struct {
	Ticker          string          `json:"ticker"`
	Amount          int64           `json:"amount"`
	PricePerStock   decimal.Decimal `json:"pricePerStock"`
	Currency        string          `json:"currency"`
	Premium         decimal.Decimal `json:"premium"`
	PremiumCurrency string          `json:"premiumCurrency"`
	SettlementDate  string          `json:"settlementDate"` // ISO-8601 date
	LastModifiedBy  ForeignBankId   `json:"lastModifiedBy"`
}

// OtcNegotiation is the full negotiation record (offer + meta).
type OtcNegotiation struct {
	ID         ForeignBankId `json:"id"`
	BuyerID    ForeignBankId `json:"buyerId"`
	SellerID   ForeignBankId `json:"sellerId"`
	Offer      OtcOffer      `json:"offer"`
	Status     string        `json:"status"` // ongoing | accepted | cancelled | expired
	UpdatedAt  string        `json:"updatedAt"` // ISO-8601
}

// OptionDescription is the SI-TX `assetId` shape for option-contract
// postings inside a NEW_TX. When acceptance triggers TX formation, the
// 4 postings reference the option's terms via this struct (encoded as
// JSON in the assetId field per cohort convention).
type OptionDescription struct {
	Ticker         string          `json:"ticker"`
	Amount         int64           `json:"amount"`
	StrikePrice    decimal.Decimal `json:"strikePrice"`
	Currency       string          `json:"currency"`
	SettlementDate string          `json:"settlementDate"`
	NegotiationID  ForeignBankId   `json:"negotiationId"`
}

// UserInformation is the response shape of GET /user/{rid}/{id}.
type UserInformation struct {
	ID        ForeignBankId `json:"id"`
	FirstName string        `json:"firstName"`
	LastName  string        `json:"lastName"`
}

// PublicStocksResponse is the response shape of GET /public-stock.
type PublicStocksResponse struct {
	Stocks []PublicStock `json:"stocks"`
}

// PublicStock is one entry in PublicStocksResponse — a stock holding the
// owner has flagged as public on this bank, available for OTC offers
// from peer banks.
type PublicStock struct {
	OwnerID       ForeignBankId   `json:"ownerId"`
	Ticker        string          `json:"ticker"`
	Amount        int64           `json:"amount"`
	PricePerStock decimal.Decimal `json:"pricePerStock"`
	Currency      string          `json:"currency"`
}
```

- [ ] **Step 1.3: Run, expect 3 PASS; commit**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
cd contract && go test ./sitx/... -count=1 -v
git add contract/sitx/otc_types.go contract/sitx/otc_types_test.go
git commit -m "feat(contract): add SI-TX OTC wire types (Phase 4 Task 1)"
```

---

### Task 2 — `PeerOTCService` proto + regen

**Files:**
- Modify: `contract/proto/stock/stock.proto`
- Regen: `contract/stockpb/`

- [ ] **Step 2.1: Append to stock.proto**

After the existing `service OTCOptionsService` block, append:

```protobuf
// PeerOTCService backs the api-gateway /api/v3/public-stock and
// /api/v3/negotiations/{rid}/{id} routes (Phase 4, Celina 5 SI-TX).
service PeerOTCService {
  rpc GetPublicStocks(GetPublicStocksRequest) returns (GetPublicStocksResponse);
  rpc CreateNegotiation(CreateNegotiationRequest) returns (CreateNegotiationResponse);
  rpc UpdateNegotiation(UpdateNegotiationRequest) returns (UpdateNegotiationResponse);
  rpc GetNegotiation(GetNegotiationRequest) returns (GetNegotiationResponse);
  rpc DeleteNegotiation(DeleteNegotiationRequest) returns (DeleteNegotiationResponse);
  rpc AcceptNegotiation(AcceptNegotiationRequest) returns (AcceptNegotiationResponse);
}

message PeerForeignBankId {
  int64 routing_number = 1;
  string id = 2;
}

message PeerOtcOffer {
  string ticker = 1;
  int64 amount = 2;
  string price_per_stock = 3;     // decimal as string
  string currency = 4;
  string premium = 5;              // decimal as string
  string premium_currency = 6;
  string settlement_date = 7;      // ISO-8601 date
  PeerForeignBankId last_modified_by = 8;
}

message GetPublicStocksRequest {
  string peer_bank_code = 1;
}

message PeerPublicStock {
  PeerForeignBankId owner_id = 1;
  string ticker = 2;
  int64 amount = 3;
  string price_per_stock = 4;
  string currency = 5;
}

message GetPublicStocksResponse {
  repeated PeerPublicStock stocks = 1;
}

message CreateNegotiationRequest {
  string peer_bank_code = 1;
  PeerOtcOffer offer = 2;
  PeerForeignBankId buyer_id = 3;
  PeerForeignBankId seller_id = 4;
}

message CreateNegotiationResponse {
  PeerForeignBankId negotiation_id = 1;
}

message UpdateNegotiationRequest {
  string peer_bank_code = 1;
  PeerForeignBankId negotiation_id = 2;
  PeerOtcOffer offer = 3;
}

message UpdateNegotiationResponse {}

message GetNegotiationRequest {
  string peer_bank_code = 1;
  PeerForeignBankId negotiation_id = 2;
}

message GetNegotiationResponse {
  PeerForeignBankId id = 1;
  PeerForeignBankId buyer_id = 2;
  PeerForeignBankId seller_id = 3;
  PeerOtcOffer offer = 4;
  string status = 5;
  string updated_at = 6;
}

message DeleteNegotiationRequest {
  string peer_bank_code = 1;
  PeerForeignBankId negotiation_id = 2;
}

message DeleteNegotiationResponse {}

message AcceptNegotiationRequest {
  string peer_bank_code = 1;
  PeerForeignBankId negotiation_id = 2;
}

message AcceptNegotiationResponse {
  string transaction_id = 1; // SI-TX TX kicked off by acceptance
  string status = 2;
}
```

- [ ] **Step 2.2: Regen + verify**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
make proto
grep -c "PeerOTCServiceServer\|RegisterPeerOTCServiceServer" contract/stockpb/stock_grpc.pb.go
```
Expected: ≥ 2.

- [ ] **Step 2.3: make build clean + commit**

```bash
make build
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(contract): add PeerOTCService proto (Phase 4 Task 2)"
```

---

### Task 3 — Add `InitiateOutboundTxWithPostings` to PeerTxService

**Why:** OTC accept needs to dispatch a 4-posting NEW_TX. The existing `InitiateOutboundTx` is hardcoded for the 2-posting transfer shape.

**Files:**
- Modify: `contract/proto/transaction/transaction.proto`
- Modify: `transaction-service/internal/handler/peer_tx_grpc_handler.go` + `_test.go`

- [ ] **Step 3.1: Add proto messages + RPC**

In the existing `service PeerTxService` block, add:
```protobuf
rpc InitiateOutboundTxWithPostings(SiTxInitiateWithPostingsRequest) returns (SiTxInitiateResponse);
```

After the existing `SiTxInitiateResponse` message, append:
```protobuf
message SiTxInitiateWithPostingsRequest {
  string peer_bank_code = 1;
  repeated SiTxPosting postings = 2;
  string tx_kind = 3; // "transfer" | "otc-accept" | "otc-exercise"
}
```

- [ ] **Step 3.2: Regen + build**

```bash
make proto
make build
```

- [ ] **Step 3.3: Implement handler method**

In `transaction-service/internal/handler/peer_tx_grpc_handler.go`, add a method on `PeerTxGRPCHandler`:

```go
// InitiateOutboundTxWithPostings is the OTC-friendly variant of
// InitiateOutboundTx — accepts a pre-composed posting list (typically
// 4 postings for OTC accept: premium money + 1× OptionDescription both
// directions) instead of building from {fromAccount, toAccount, amount}.
//
// Reuses the same outbound_peer_txs / PeerHTTPClient / OutboundReplayCron
// flow as the simple-transfer InitiateOutboundTx; the only difference is
// the postings come from the caller and the tx_kind column reflects the
// originating intent (transfer / otc-accept / otc-exercise).
func (h *PeerTxGRPCHandler) InitiateOutboundTxWithPostings(ctx context.Context, req *transactionpb.SiTxInitiateWithPostingsRequest) (*transactionpb.SiTxInitiateResponse, error) {
	if h.outRepo == nil || h.httpClient == nil || h.peerLookup == nil {
		return nil, status.Error(codes.Unimplemented, "outbound deps not wired")
	}
	if len(req.GetPostings()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "postings required")
	}
	target, err := h.peerLookup(ctx, req.GetPeerBankCode())
	if err != nil || target == nil {
		return nil, status.Errorf(codes.NotFound, "peer bank %s not registered", req.GetPeerBankCode())
	}

	idem := uuid.NewString()
	postings := make([]contractsitx.Posting, 0, len(req.GetPostings()))
	for _, p := range req.GetPostings() {
		amt, _ := decimal.NewFromString(p.GetAmount())
		postings = append(postings, contractsitx.Posting{
			RoutingNumber: p.GetRoutingNumber(),
			AccountID:     p.GetAccountId(),
			AssetID:       p.GetAssetId(),
			Amount:        amt,
			Direction:     p.GetDirection(),
		})
	}
	postingsJSON, _ := json.Marshal(postings)

	txKind := req.GetTxKind()
	if txKind == "" {
		txKind = "transfer"
	}
	row := &model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   req.GetPeerBankCode(),
		TxKind:         txKind,
		PostingsJSON:   string(postingsJSON),
		Status:         "pending",
	}
	if err := h.outRepo.Create(row); err != nil {
		return nil, status.Errorf(codes.Internal, "outbound row: %v", err)
	}

	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message:        contractsitx.Transaction{Postings: postings},
	}
	if vote, err := h.httpClient.PostNewTx(ctx, target, envelope); err != nil {
		_ = h.outRepo.MarkAttempt(idem, err.Error())
	} else if vote.Type == contractsitx.VoteYes {
		commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
			IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
			MessageType:    contractsitx.MessageTypeCommitTx,
			Message:        contractsitx.CommitTransaction{TransactionID: idem},
		}
		if err := h.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
			_ = h.outRepo.MarkAttempt(idem, "commit: "+err.Error())
		} else {
			_ = h.outRepo.MarkCommitted(idem)
		}
	} else {
		reason := "peer voted NO"
		if len(vote.NoVotes) > 0 {
			reason = "peer voted NO: " + vote.NoVotes[0].Reason
		}
		_ = h.outRepo.MarkRolledBack(idem, reason)
	}

	return &transactionpb.SiTxInitiateResponse{
		TransactionId: idem,
		PollUrl:       "/api/v3/me/transfers/" + idem, // OTC also uses this poll endpoint for now
		Status:        "pending",
	}, nil
}
```

- [ ] **Step 3.4: Add a test for the new RPC** (matches `TestInitiateOutboundTx_HappyPath` shape from existing tests, but feeds 4 postings).

- [ ] **Step 3.5: Build + test + commit**

```bash
cd transaction-service && go build ./... && go test ./... -count=1
git add contract/proto/transaction/transaction.proto contract/transactionpb/ \
        transaction-service/internal/handler/peer_tx_grpc_handler.go \
        transaction-service/internal/handler/peer_tx_grpc_handler_test.go
git commit -m "feat(transaction-service): add InitiateOutboundTxWithPostings (Phase 4 Task 3)"
```

---

### Task 4 — `peer_otc_negotiations` model + repository

**Files:**
- Create: `stock-service/internal/model/peer_otc_negotiation.go`
- Create: `stock-service/internal/repository/peer_otc_negotiation_repository.go` + `_test.go`

- [ ] **Step 4.1: Model**

```go
package model

import "time"

// PeerOtcNegotiation is the receiver-side persistence of an OTC negotiation
// initiated by a peer bank. Created when a peer POSTs to /negotiations,
// updated on counter-offers (PUT) and acceptance status changes.
type PeerOtcNegotiation struct {
	ID                  uint64    `gorm:"primaryKey"`
	PeerBankCode        string    `gorm:"size:8;not null;index"`
	ForeignID           string    `gorm:"size:128;not null"` // peer's id for this negotiation
	BuyerRoutingNumber  int64     `gorm:"not null"`
	BuyerID             string    `gorm:"size:128;not null"`
	SellerRoutingNumber int64     `gorm:"not null"`
	SellerID            string    `gorm:"size:128;not null"`
	OfferJSON           string    `gorm:"type:text;not null"` // serialised contractsitx.OtcOffer
	Status              string    `gorm:"size:32;not null;default:ongoing;index"`
	CreatedAt           time.Time `gorm:"not null"`
	UpdatedAt           time.Time `gorm:"not null"`
}
```

- [ ] **Step 4.2: Repository (CRUD + GetByPeerAndID)**

```go
package repository

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

type PeerOtcNegotiationRepository struct {
	db *gorm.DB
}

func NewPeerOtcNegotiationRepository(db *gorm.DB) *PeerOtcNegotiationRepository {
	return &PeerOtcNegotiationRepository{db: db}
}

func (r *PeerOtcNegotiationRepository) Create(neg *model.PeerOtcNegotiation) error {
	return r.db.Create(neg).Error
}

func (r *PeerOtcNegotiationRepository) GetByPeerAndID(peerCode, foreignID string) (*model.PeerOtcNegotiation, error) {
	var neg model.PeerOtcNegotiation
	if err := r.db.Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).First(&neg).Error; err != nil {
		return nil, err
	}
	return &neg, nil
}

func (r *PeerOtcNegotiationRepository) UpdateOffer(peerCode, foreignID, offerJSON string) error {
	return r.db.Model(&model.PeerOtcNegotiation{}).
		Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Updates(map[string]interface{}{"offer_json": offerJSON}).Error
}

func (r *PeerOtcNegotiationRepository) UpdateStatus(peerCode, foreignID, status string) error {
	return r.db.Model(&model.PeerOtcNegotiation{}).
		Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Updates(map[string]interface{}{"status": status}).Error
}

func (r *PeerOtcNegotiationRepository) Delete(peerCode, foreignID string) error {
	return r.db.Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Delete(&model.PeerOtcNegotiation{}).Error
}
```

- [ ] **Step 4.3: Tests** (4 cases: Create+Get, GetNotFound, UpdateOffer+Status, Delete). Build + lint + commit.

---

### Task 5 — `PeerOTCGRPCHandler` (stock-service)

Implements the 6 PeerOTCService RPCs. `GetPublicStocks` reads from existing `Holding` rows where `Public = true`. `CreateNegotiation` / `Update` / `Get` / `Delete` use the new `peer_otc_negotiations` table. `AcceptNegotiation` composes 4 postings and calls `transaction-service.PeerTxService.InitiateOutboundTxWithPostings`.

**Files:**
- Create: `stock-service/internal/handler/peer_otc_grpc_handler.go` + `_test.go`

- [ ] **Step 5.1: Skeleton + GetPublicStocks** — query `holdings` table for `public = true` rows, return PeerPublicStock list.

- [ ] **Step 5.2: CreateNegotiation** — insert into `peer_otc_negotiations`, return generated id.

- [ ] **Step 5.3: UpdateNegotiation / GetNegotiation / DeleteNegotiation** — straightforward repo wrappers.

- [ ] **Step 5.4: AcceptNegotiation** — compose 4 postings (premium money debit-buyer / credit-seller, OptionDescription as assetID debit-seller-asset / credit-buyer-asset), call `peerTxClient.InitiateOutboundTxWithPostings`, return tx id.

- [ ] **Step 5.5: Tests** — 5 cases (Create, Get-with-existing, Update, Delete, Accept dispatches via stub PeerTxClient).

- [ ] **Step 5.6: Build + commit**

---

### Task 6 — Wire stock-service `cmd/main.go`

- [ ] AutoMigrate `&model.PeerOtcNegotiation{}`.
- [ ] Construct `peerOtcRepo := repository.NewPeerOtcNegotiationRepository(db)`.
- [ ] gRPC client to transaction-service PeerTxService (for AcceptNegotiation dispatch).
- [ ] Construct `peerOtcHandler := handler.NewPeerOTCGRPCHandler(peerOtcRepo, holdingRepo, peerTxClient)`.
- [ ] `pb.RegisterPeerOTCServiceServer(s, peerOtcHandler)`.
- [ ] Build + commit.

---

### Task 7 — api-gateway PeerOTCServiceClient + handlers

**Files:**
- Create: `api-gateway/internal/grpc/peer_otc_client.go`
- Create: `api-gateway/internal/handler/peer_otc_handler.go` + `_test.go`
- Create: `api-gateway/internal/handler/peer_user_handler.go` + `_test.go`

- [ ] **Step 7.1: gRPC client wrapper** — `NewPeerOTCServiceClient(addr)`, mirrors `NewPeerTxServiceClient`.

- [ ] **Step 7.2: PeerOTCHandler** — 6 methods (GetPublicStocks, CreateNegotiation, UpdateNegotiation, GetNegotiation, DeleteNegotiation, AcceptNegotiation). Each:
  - Reads `peer_bank_code` from gin context (set by PeerAuth middleware).
  - Decodes path params (rid + id) for negotiation routes.
  - Decodes body (POST/PUT) into `contractsitx.OtcOffer`.
  - Calls gRPC, maps response to JSON.

- [ ] **Step 7.3: PeerUserHandler** — `GET /api/v3/user/{rid}/{id}`. Routes by rid: if rid == OWN_ROUTING, look up via user-service or client-service (peers asking us for our user's info). Otherwise return 404 (we don't proxy lookups across banks). Returns `contractsitx.UserInformation`.

- [ ] **Step 7.4: Tests** — table-driven for each route (4 + 5 + 1 = 10 cases minimum).

- [ ] **Step 7.5: Commit**

---

### Task 8 — Wire api-gateway router + main.go

- [ ] Add `PeerOTCClient transactionpb.PeerOTCServiceClient` (actually `stockpb.PeerOTCServiceClient`) to Deps.
- [ ] Add `PeerOTC *handler.PeerOTCHandler` and `PeerUser *handler.PeerUserHandler` to Handlers.
- [ ] In `NewHandlers`, construct both.
- [ ] In `router_v3.go` `peer` group (already exists from Phase 2 Task 14):
  ```go
  peer.GET("/public-stock", h.PeerOTC.GetPublicStocks)
  peer.POST("/negotiations", h.PeerOTC.CreateNegotiation)
  peer.PUT("/negotiations/:rid/:id", h.PeerOTC.UpdateNegotiation)
  peer.GET("/negotiations/:rid/:id", h.PeerOTC.GetNegotiation)
  peer.DELETE("/negotiations/:rid/:id", h.PeerOTC.DeleteNegotiation)
  peer.GET("/negotiations/:rid/:id/accept", h.PeerOTC.AcceptNegotiation)
  peer.GET("/user/:rid/:id", h.PeerUser.GetUser)
  ```
- [ ] In `cmd/main.go`, construct gRPC client to stock-service for `PeerOTCService` and pass through Deps.
- [ ] Build + workspace test + commit.

---

### Task 9 — REST_API_v3.md docs

- [ ] Add a new subsection in §38 (Peer-Bank Protocol) documenting the 7 new peer-facing routes. Each: auth (PeerAuth), method, path, request body, response. Phase 4 status note.
- [ ] Commit.

---

### Task 10 — Final verification

- [ ] `make build / test / lint` clean.
- [ ] Update memory with Phase 4 completion.
- [ ] Done.
