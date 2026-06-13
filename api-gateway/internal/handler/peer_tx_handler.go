package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerTxHandler serves POST /api/v3/interbank. It decodes the SI-TX
// Message<Type> envelope, dispatches by messageType to the matching
// PeerTxService RPC, and renders the response per SI-TX HTTP-status
// rules (200 with body for vote, 204 for ack, 501 when the gRPC
// backend returns Unimplemented).
type PeerTxHandler struct {
	client transactionpb.PeerTxServiceClient
}

func NewPeerTxHandler(c transactionpb.PeerTxServiceClient) *PeerTxHandler {
	return &PeerTxHandler{client: c}
}

// PostInterbank godoc
// @Summary      Peer-to-peer: SI-TX wire entry (NEW_TX / COMMIT_TX / ROLLBACK_TX / VOTE)
// @Description  Inbound from a peer bank. Authenticated via PeerAuth (X-Api-Key or HMAC). Forwards the envelope to transaction-service which classifies on `type` and dispatches.
// @Tags         PeerOTC
// @Accept       json
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/interbank [post]
func (h *PeerTxHandler) PostInterbank(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var head struct {
		IdempotenceKey sitx.IdempotenceKey `json:"idempotenceKey"`
		MessageType    string              `json:"messageType"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if head.IdempotenceKey.LocallyGeneratedKey == "" || head.MessageType == "" {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	peerBankCode, _ := c.Get("peer_bank_code")
	pbCode, _ := peerBankCode.(string)

	switch head.MessageType {
	case sitx.MessageTypeNewTx:
		var msg sitx.Message[sitx.Transaction]
		if err := json.Unmarshal(body, &msg); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		postings, err := specPostingsToProto(msg.Message.Postings)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
		req := &transactionpb.SiTxNewTxRequest{
			IdempotenceKey: idemToProto(msg.IdempotenceKey),
			PeerBankCode:   pbCode,
			Postings:       postings,
			TransactionId:  fbIDToProto(msg.Message.TransactionID),
			Message:        msg.Message.Message,
			PaymentCode:    msg.Message.PaymentCode,
			PaymentPurpose: msg.Message.PaymentPurpose,
			CallNumber:     msg.Message.CallNumber,
		}
		resp, err := h.client.HandleNewTx(c.Request.Context(), req)
		if err != nil {
			renderPeerGRPCError(c, err)
			return
		}
		if resp.GetPending() {
			// SI-TX §2.11: accepted, still processing; sender retransmits.
			// AbortWithStatus flushes the header immediately (empty body).
			c.AbortWithStatus(http.StatusAccepted)
			return
		}
		vote := sitx.TransactionVote{Vote: resp.GetType()}
		for _, nv := range resp.GetNoVotes() {
			r := sitx.NoVoteReason{Reason: nv.GetReason()}
			if nv.GetPostingIndexSet() {
				idx := int(nv.GetPostingIndex())
				if idx >= 0 && idx < len(msg.Message.Postings) {
					p := msg.Message.Postings[idx]
					r.Posting = &p
				}
			}
			vote.Reasons = append(vote.Reasons, r)
		}
		c.JSON(http.StatusOK, vote)
	case sitx.MessageTypeCommitTx:
		var msg sitx.Message[sitx.CommitTransaction]
		if err := json.Unmarshal(body, &msg); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		_, err := h.client.HandleCommitTx(c.Request.Context(), &transactionpb.SiTxCommitRequest{
			IdempotenceKey: idemToProto(msg.IdempotenceKey),
			PeerBankCode:   pbCode,
			TransactionId:  fbIDToProto(msg.Message.TransactionID),
		})
		if err != nil {
			renderPeerGRPCError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	case sitx.MessageTypeRollbackTx:
		var msg sitx.Message[sitx.RollbackTransaction]
		if err := json.Unmarshal(body, &msg); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		_, err := h.client.HandleRollbackTx(c.Request.Context(), &transactionpb.SiTxRollbackRequest{
			IdempotenceKey: idemToProto(msg.IdempotenceKey),
			PeerBankCode:   pbCode,
			TransactionId:  fbIDToProto(msg.Message.TransactionID),
		})
		if err != nil {
			renderPeerGRPCError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	default:
		c.AbortWithStatus(http.StatusBadRequest)
	}
}

func idemToProto(k sitx.IdempotenceKey) *transactionpb.SiTxIdempotenceKey {
	return &transactionpb.SiTxIdempotenceKey{
		RoutingNumber:       k.RoutingNumber,
		LocallyGeneratedKey: k.LocallyGeneratedKey,
	}
}

// specPostingsToProto translates spec-shaped postings (tagged unions, signed
// amount) to the enriched internal proto via sitx.SpecPostingToInternal, which
// applies the sign→direction inversion and carries the account/asset type tags.
func specPostingsToProto(ps []sitx.Posting) ([]*transactionpb.SiTxPosting, error) {
	out := make([]*transactionpb.SiTxPosting, len(ps))
	for i, p := range ps {
		ip, err := sitx.SpecPostingToInternal(p)
		if err != nil {
			return nil, err
		}
		out[i] = &transactionpb.SiTxPosting{
			RoutingNumber: ip.RoutingNumber,
			AccountId:     ip.AccountID,
			AssetId:       ip.AssetID,
			Amount:        ip.Amount,
			Direction:     ip.Direction,
			AccountType:   ip.AccountType,
			AssetType:     ip.AssetType,
		}
	}
	return out, nil
}

func fbIDToProto(f sitx.ForeignBankId) *transactionpb.SiTxForeignBankId {
	return &transactionpb.SiTxForeignBankId{RoutingNumber: f.RoutingNumber, Id: f.ID}
}

// renderPeerGRPCError maps gRPC status codes to SI-TX HTTP semantics:
//   - Unimplemented → 501 (Phase 2 default for stub PeerTxService)
//   - InvalidArgument → 400
//   - everything else → 500
func renderPeerGRPCError(c *gin.Context, err error) {
	st, ok := status.FromError(err)
	if !ok {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	switch st.Code() {
	case codes.Unimplemented:
		c.AbortWithStatus(http.StatusNotImplemented)
	case codes.InvalidArgument:
		c.AbortWithStatus(http.StatusBadRequest)
	default:
		c.AbortWithStatus(http.StatusInternalServerError)
	}
}
