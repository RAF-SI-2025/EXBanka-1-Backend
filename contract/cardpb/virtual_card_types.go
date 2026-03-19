// Hand-written gRPC types for virtual card, PIN, and temporary block RPCs.
package cardpb

// CreateVirtualCardRequest is used to create a virtual card.
type CreateVirtualCardRequest struct {
	AccountNumber string `json:"account_number"`
	OwnerId       uint64 `json:"owner_id"`
	CardBrand     string `json:"card_brand"`
	UsageType     string `json:"usage_type"`  // "single_use" or "multi_use"
	MaxUses       int32  `json:"max_uses"`
	ExpiryMonths  int32  `json:"expiry_months"` // 1, 2, or 3
	CardLimit     string `json:"card_limit"`
}

// SetCardPinRequest is used to set a card PIN.
type SetCardPinRequest struct {
	Id  uint64 `json:"id"`
	Pin string `json:"pin"`
}

// SetCardPinResponse is returned after setting a PIN.
type SetCardPinResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// VerifyCardPinRequest is used to verify a card PIN.
type VerifyCardPinRequest struct {
	Id  uint64 `json:"id"`
	Pin string `json:"pin"`
}

// VerifyCardPinResponse is returned after verifying a PIN.
type VerifyCardPinResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

// TemporaryBlockCardRequest is used to temporarily block a card.
type TemporaryBlockCardRequest struct {
	Id            uint64 `json:"id"`
	DurationHours int32  `json:"duration_hours"`
	Reason        string `json:"reason"`
}

// UseCardRequest is used to decrement virtual card uses.
type UseCardRequest struct {
	Id uint64 `json:"id"`
}

// UseCardResponse is returned after using a card.
type UseCardResponse struct {
	Success bool `json:"success"`
}
