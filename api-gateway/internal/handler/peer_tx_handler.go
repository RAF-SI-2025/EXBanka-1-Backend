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
		req := &transactionpb.SiTxNewTxRequest{
			IdempotenceKey: idemToProto(msg.IdempotenceKey),
			PeerBankCode:   pbCode,
			Postings:       postingsToProto(msg.Message.Postings),
		}
		resp, err := h.client.HandleNewTx(c.Request.Context(), req)
		if err != nil {
			renderPeerGRPCError(c, err)
			return
		}
		c.JSON(http.StatusOK, voteToJSON(resp))
	case sitx.MessageTypeCommitTx:
		var msg sitx.Message[sitx.CommitTransaction]
		if err := json.Unmarshal(body, &msg); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		_, err := h.client.HandleCommitTx(c.Request.Context(), &transactionpb.SiTxCommitRequest{
			IdempotenceKey: idemToProto(msg.IdempotenceKey),
			PeerBankCode:   pbCode,
			TransactionId:  msg.Message.TransactionID,
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
			TransactionId:  msg.Message.TransactionID,
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

func postingsToProto(ps []sitx.Posting) []*transactionpb.SiTxPosting {
	out := make([]*transactionpb.SiTxPosting, len(ps))
	for i, p := range ps {
		out[i] = &transactionpb.SiTxPosting{
			RoutingNumber: p.RoutingNumber,
			AccountId:     p.AccountID,
			AssetId:       p.AssetID,
			Amount:        p.Amount.String(),
			Direction:     p.Direction,
		}
	}
	return out
}

func voteToJSON(v *transactionpb.SiTxVoteResponse) sitx.TransactionVote {
	out := sitx.TransactionVote{Type: v.GetType()}
	for _, nv := range v.GetNoVotes() {
		entry := sitx.NoVote{Reason: nv.GetReason()}
		if nv.GetPostingIndexSet() {
			idx := int(nv.GetPostingIndex())
			entry.Posting = &idx
		}
		out.NoVotes = append(out.NoVotes, entry)
	}
	return out
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
