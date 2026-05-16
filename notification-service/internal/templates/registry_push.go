package templates

// pushDefs are the code-defined in-app ("push" channel) notification template
// types. Publishers emit GeneralNotificationMessage{Type, Data} and
// notification-service renders the Type via this registry.
var pushDefs = []Definition{
	// ── Orders ───────────────────────────────────────────────────────────────
	{
		Type: "ORDER_PLACED", Channel: "push",
		Description: "An order was placed.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
			{"order_type", "Order type", "market"},
		},
		DefaultSubject: "Order placed",
		DefaultBody:    "Your {{order_type}} order to {{direction}} {{quantity}} {{ticker}} has been placed.",
	},
	{
		Type: "ORDER_APPROVED", Channel: "push",
		Description: "A pending order was approved.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order approved",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} has been approved.",
	},
	{
		Type: "ORDER_DECLINED", Channel: "push",
		Description: "A pending order was declined.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order declined",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} was declined.",
	},
	{
		Type: "ORDER_PARTIALLY_FILLED", Channel: "push",
		Description: "An order was partially filled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"filled_quantity", "Units filled in this fill", "3"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order partially filled",
		DefaultBody:    "Your {{direction}} order for {{ticker}} partially filled — {{filled_quantity}} units.",
	},
	{
		Type: "ORDER_FILLED", Channel: "push",
		Description: "An order was fully filled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order filled",
		DefaultBody:    "Your {{direction}} order for {{quantity}} {{ticker}} has been fully filled.",
	},
	{
		Type: "ORDER_CANCELLED", Channel: "push",
		Description: "An order was cancelled.",
		Variables: []Variable{
			{"direction", "buy or sell", "buy"},
			{"quantity", "Number of units", "10"},
			{"ticker", "Security ticker", "AAPL"},
		},
		DefaultSubject: "Order cancelled",
		DefaultBody:    "Your order to {{direction}} {{quantity}} {{ticker}} has been cancelled.",
	},
	// ── OTC ──────────────────────────────────────────────────────────────────
	{
		Type: "OTC_OFFER_RECEIVED", Channel: "push",
		Description: "An OTC option offer was sent to you.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium", "Option premium", "5.00"},
		},
		DefaultSubject: "New OTC offer",
		DefaultBody:    "You received an OTC offer on {{ticker}} — {{quantity}} @ strike {{strike_price}}, premium {{premium}}.",
	},
	{
		Type: "OTC_OFFER_COUNTERED", Channel: "push",
		Description: "The other party countered your OTC offer.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium", "Option premium", "5.00"},
		},
		DefaultSubject: "OTC offer countered",
		DefaultBody:    "Your OTC offer on {{ticker}} was countered — now {{quantity}} @ strike {{strike_price}}, premium {{premium}}.",
	},
	{
		Type: "OTC_OFFER_REJECTED", Channel: "push",
		Description: "The other party rejected your OTC offer.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC offer rejected",
		DefaultBody:    "Your OTC offer on {{ticker}} was rejected.",
	},
	{
		Type: "OTC_OFFER_EXPIRED", Channel: "push",
		Description: "An OTC offer expired without being accepted.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC offer expired",
		DefaultBody:    "Your OTC offer on {{ticker}} has expired.",
	},
	{
		Type: "OTC_OFFER_CANCELLED", Channel: "push",
		Description: "The other party cancelled an OTC negotiation chain you were in.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC offer cancelled",
		DefaultBody:    "Your OTC negotiation on {{ticker}} was cancelled by the other party.",
	},
	{
		Type: "OTC_OFFER_CASCADE_CANCELLED", Channel: "push",
		Description: "Your bid was cancelled because the seller accepted a competing bid on the same listing.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"accepted_premium", "Premium that won the listing", "45.00"},
		},
		DefaultSubject: "OTC bid cancelled — competitor won",
		DefaultBody:    "Your bid on {{ticker}} was cancelled because the seller accepted a competing offer at premium {{accepted_premium}}.",
	},
	{
		Type: "OTC_CONTRACT_CREATED", Channel: "push",
		Description: "An OTC offer was accepted and a contract was created.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"quantity", "Contract quantity", "100"},
			{"strike_price", "Strike price", "150.00"},
			{"premium_paid", "Premium paid", "5.00"},
		},
		DefaultSubject: "OTC contract created",
		DefaultBody:    "An OTC offer was accepted — contract on {{ticker}}, {{quantity}} @ strike {{strike_price}}, premium {{premium_paid}}.",
	},
	{
		Type: "OTC_CONTRACT_EXERCISED", Channel: "push",
		Description: "An OTC option contract was exercised.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
			{"shares_transferred", "Shares transferred", "100"},
			{"strike_amount_paid", "Strike amount paid", "15000.00"},
		},
		DefaultSubject: "OTC contract exercised",
		DefaultBody:    "An OTC contract on {{ticker}} was exercised — {{shares_transferred}} shares for {{strike_amount_paid}}.",
	},
	{
		Type: "OTC_CONTRACT_EXPIRED", Channel: "push",
		Description: "An OTC option contract expired unexercised.",
		Variables: []Variable{
			{"ticker", "Underlying ticker", "AAPL"},
		},
		DefaultSubject: "OTC contract expired",
		DefaultBody:    "Your OTC contract on {{ticker}} has expired.",
	},
	{
		Type: "OTC_CONTRACT_FAILED", Channel: "push",
		Description: "An OTC option contract failed during settlement.",
		Variables: []Variable{
			{"failure_reason", "Why the contract failed", "insufficient funds"},
		},
		DefaultSubject: "OTC contract failed",
		DefaultBody:    "An OTC contract could not be completed: {{failure_reason}}.",
	},
	// ── Money movement ───────────────────────────────────────────────────────
	{
		Type: "PAYMENT_SENT", Channel: "push",
		Description: "A payment was sent from your account.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"to_account", "Destination account number", "265-2-22"},
		},
		DefaultSubject: "Payment sent",
		DefaultBody:    "{{amount}} was sent from your account to {{to_account}}.",
	},
	{
		Type: "PAYMENT_RECEIVED", Channel: "push",
		Description: "A payment was credited to your account.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"from_account", "Source account number", "265-1-11"},
		},
		DefaultSubject: "Payment received",
		DefaultBody:    "{{amount}} was credited to your account from {{from_account}}.",
	},
	{
		Type: "PAYMENT_FAILED", Channel: "push",
		Description: "A payment failed.",
		Variables: []Variable{
			{"amount", "Payment amount", "1000.00 RSD"},
			{"failure_reason", "Why the payment failed", "insufficient funds"},
		},
		DefaultSubject: "Payment failed",
		DefaultBody:    "Your payment of {{amount}} failed: {{failure_reason}}.",
	},
	{
		Type: "TRANSFER_SENT", Channel: "push",
		Description: "A transfer was sent from your account.",
		Variables: []Variable{
			{"amount", "Transfer amount", "1000.00 RSD"},
			{"to_account", "Destination account number", "265-2-22"},
		},
		DefaultSubject: "Transfer sent",
		DefaultBody:    "{{amount}} was transferred from your account to {{to_account}}.",
	},
	{
		Type: "TRANSFER_RECEIVED", Channel: "push",
		Description: "A transfer was credited to your account.",
		Variables: []Variable{
			{"final_amount", "Amount credited", "1000.00 RSD"},
			{"from_account", "Source account number", "265-1-11"},
		},
		DefaultSubject: "Transfer received",
		DefaultBody:    "{{final_amount}} was credited to your account from {{from_account}}.",
	},
	{
		Type: "TRANSFER_FAILED", Channel: "push",
		Description: "A transfer failed.",
		Variables: []Variable{
			{"amount", "Transfer amount", "1000.00 RSD"},
			{"failure_reason", "Why the transfer failed", "insufficient funds"},
		},
		DefaultSubject: "Transfer failed",
		DefaultBody:    "Your transfer of {{amount}} failed: {{failure_reason}}.",
	},
	// ── Loans ────────────────────────────────────────────────────────────────
	{
		Type: "LOAN_REQUEST_SUBMITTED", Channel: "push",
		Description: "A loan request was submitted.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
		},
		DefaultSubject: "Loan request submitted",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} has been submitted.",
	},
	{
		Type: "LOAN_REQUEST_APPROVED", Channel: "push",
		Description: "A loan request was approved.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Approved amount", "500000.00"},
		},
		DefaultSubject: "Loan approved",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} has been approved.",
	},
	{
		Type: "LOAN_REQUEST_REJECTED", Channel: "push",
		Description: "A loan request was rejected.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
		},
		DefaultSubject: "Loan request rejected",
		DefaultBody:    "Your {{loan_type}} loan request for {{amount}} was not approved.",
	},
	{
		Type: "LOAN_DISBURSED", Channel: "push",
		Description: "Loan funds were disbursed.",
		Variables: []Variable{
			{"loan_number", "Loan number", "LN-000123"},
			{"amount", "Disbursed amount", "500000.00"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Loan disbursed",
		DefaultBody:    "Loan {{loan_number}} has been disbursed — {{amount}} {{currency}}.",
	},
	{
		Type: "INSTALLMENT_COLLECTED", Channel: "push",
		Description: "A loan installment was collected.",
		Variables: []Variable{
			{"amount", "Installment amount", "12500.00"},
		},
		DefaultSubject: "Installment paid",
		DefaultBody:    "A loan installment of {{amount}} has been collected.",
	},
	{
		Type: "INSTALLMENT_FAILED", Channel: "push",
		Description: "A loan installment collection failed.",
		Variables: []Variable{
			{"amount", "Installment amount", "12500.00"},
			{"retry_deadline", "Retry deadline", "2026-06-01"},
		},
		DefaultSubject: "Installment payment failed",
		DefaultBody:    "We couldn't collect a loan installment of {{amount}}. Please ensure funds are available by {{retry_deadline}}.",
	},
	// ── Accounts ─────────────────────────────────────────────────────────────
	{
		Type: "ACCOUNT_OPENED", Channel: "push",
		Description: "A new account was opened.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Account opened",
		DefaultBody:    "Your new {{currency}} account {{account_number}} has been opened.",
	},
	{
		Type: "ACCOUNT_STATUS_CHANGED", Channel: "push",
		Description: "An account's status changed.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"new_status", "New account status", "inactive"},
		},
		DefaultSubject: "Account status changed",
		DefaultBody:    "The status of account {{account_number}} changed to {{new_status}}.",
	},
	{
		Type: "ACCOUNT_NAME_UPDATED", Channel: "push",
		Description: "An account's name was updated.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"new_name", "New account name", "Savings"},
		},
		DefaultSubject: "Account name updated",
		DefaultBody:    "Account {{account_number}} was renamed to \"{{new_name}}\".",
	},
	{
		Type: "ACCOUNT_LIMITS_UPDATED", Channel: "push",
		Description: "An account's spending limits were updated.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"daily_limit", "New daily limit", "100000.00"},
			{"monthly_limit", "New monthly limit", "1000000.00"},
		},
		DefaultSubject: "Account limits updated",
		DefaultBody:    "Limits for account {{account_number}} updated — daily {{daily_limit}}, monthly {{monthly_limit}}.",
	},
	{
		Type: "MAINTENANCE_FEE_CHARGED", Channel: "push",
		Description: "A maintenance fee was charged.",
		Variables: []Variable{
			{"account_number", "Account number", "265-1-11"},
			{"amount", "Fee amount", "300.00"},
			{"currency", "Currency code", "RSD"},
		},
		DefaultSubject: "Maintenance fee charged",
		DefaultBody:    "A maintenance fee of {{amount}} {{currency}} was charged to account {{account_number}}.",
	},
	// ── Cards ────────────────────────────────────────────────────────────────
	{
		Type: "CARD_CREATED", Channel: "push",
		Description: "A new card was issued.",
		Variables: []Variable{
			{"card_brand", "Card brand", "visa"},
		},
		DefaultSubject: "Card issued",
		DefaultBody:    "A new {{card_brand}} card has been issued for your account.",
	},
	{
		Type: "CARD_STATUS_CHANGED", Channel: "push",
		Description: "A card's status changed.",
		Variables: []Variable{
			{"new_status", "New card status", "blocked"},
		},
		DefaultSubject: "Card status changed",
		DefaultBody:    "Your card status changed to {{new_status}}.",
	},
	{
		Type: "CARD_TEMPORARY_BLOCKED", Channel: "push",
		Description: "A card was temporarily blocked.",
		Variables: []Variable{
			{"expires_at", "When the block lifts", "2026-06-01"},
			{"reason", "Block reason", "customer request"},
		},
		DefaultSubject: "Card temporarily blocked",
		DefaultBody:    "Your card has been temporarily blocked ({{reason}}) until {{expires_at}}.",
	},
	{
		Type: "VIRTUAL_CARD_CREATED", Channel: "push",
		Description: "A virtual card was created.",
		Variables: []Variable{
			{"usage_type", "Virtual card usage type", "single_use"},
		},
		DefaultSubject: "Virtual card created",
		DefaultBody:    "A {{usage_type}} virtual card has been created for your account.",
	},
	{
		Type: "CARD_REQUEST_CREATED", Channel: "push",
		Description: "A card request was submitted.",
		Variables: []Variable{
			{"card_brand", "Requested card brand", "visa"},
		},
		DefaultSubject: "Card request submitted",
		DefaultBody:    "Your request for a {{card_brand}} card has been submitted.",
	},
	{
		Type: "CARD_REQUEST_APPROVED", Channel: "push",
		Description: "A card request was approved.",
		Variables: []Variable{
			{"card_brand", "Requested card brand", "visa"},
		},
		DefaultSubject: "Card request approved",
		DefaultBody:    "Your {{card_brand}} card request has been approved.",
	},
	{
		Type: "CARD_REQUEST_REJECTED", Channel: "push",
		Description: "A card request was rejected.",
		Variables: []Variable{
			{"reason", "Rejection reason", "incomplete documentation"},
		},
		DefaultSubject: "Card request rejected",
		DefaultBody:    "Your card request was rejected: {{reason}}.",
	},
}
