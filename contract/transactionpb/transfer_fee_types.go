// Hand-written gRPC types for transfer fee management.
package transactionpb

// TransferFeeResponse is a fee rule.
type TransferFeeResponse struct {
	Id              uint64 `json:"id"`
	Name            string `json:"name"`
	FeeType         string `json:"fee_type"`
	FeeValue        string `json:"fee_value"`
	MinAmount       string `json:"min_amount"`
	MaxFee          string `json:"max_fee"`
	TransactionType string `json:"transaction_type"`
	CurrencyCode    string `json:"currency_code"`
	Active          bool   `json:"active"`
}

// ListFeesRequest is empty.
type ListFeesRequest struct{}

// ListFeesResponse contains all fee rules.
type ListFeesResponse struct {
	Fees []*TransferFeeResponse `json:"fees"`
}

// CreateFeeRequest creates a new fee rule.
type CreateFeeRequest struct {
	Name            string `json:"name"`
	FeeType         string `json:"fee_type"`
	FeeValue        string `json:"fee_value"`
	MinAmount       string `json:"min_amount"`
	MaxFee          string `json:"max_fee"`
	TransactionType string `json:"transaction_type"`
	CurrencyCode    string `json:"currency_code"`
}

// UpdateFeeRequest updates a fee rule.
type UpdateFeeRequest struct {
	Id              uint64 `json:"id"`
	Name            string `json:"name"`
	FeeType         string `json:"fee_type"`
	FeeValue        string `json:"fee_value"`
	MinAmount       string `json:"min_amount"`
	MaxFee          string `json:"max_fee"`
	TransactionType string `json:"transaction_type"`
	CurrencyCode    string `json:"currency_code"`
	Active          bool   `json:"active"`
}

// DeleteFeeRequest deactivates a fee rule.
type DeleteFeeRequest struct {
	Id uint64 `json:"id"`
}

// DeleteFeeResponse is returned after deactivation.
type DeleteFeeResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
