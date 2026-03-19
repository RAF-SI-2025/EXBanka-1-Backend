// Hand-written gRPC types for bank account management RPCs.
package accountpb

// CreateBankAccountRequest creates a bank-owned account.
type CreateBankAccountRequest struct {
	CurrencyCode string `json:"currency_code"`
	AccountKind  string `json:"account_kind"`
	AccountName  string `json:"account_name"`
}

// ListBankAccountsRequest is empty (lists all bank accounts).
type ListBankAccountsRequest struct{}

// ListBankAccountsResponse contains the list of bank accounts.
type ListBankAccountsResponse struct {
	Accounts []*AccountResponse `json:"accounts"`
}

// DeleteBankAccountRequest deletes a bank account by ID.
type DeleteBankAccountRequest struct {
	Id uint64 `json:"id"`
}

// DeleteBankAccountResponse is returned after deletion.
type DeleteBankAccountResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// GetBankRSDAccountRequest fetches the bank's primary RSD account.
type GetBankRSDAccountRequest struct{}
