// contract/kafka/messages.go
package kafka

const (
	TopicSendEmail = "notification.send-email"
	TopicEmailSent = "notification.email-sent"
	TopicSendPush  = "notification.send-push"
	TopicPushSent  = "notification.push-sent"
)

type EmailType string

const (
	EmailTypeActivation    EmailType = "ACTIVATION"
	EmailTypePasswordReset EmailType = "PASSWORD_RESET"
	EmailTypeConfirmation  EmailType = "CONFIRMATION"
)

type SendEmailMessage struct {
	To        string            `json:"to"`
	EmailType EmailType         `json:"email_type"`
	Data      map[string]string `json:"data"`
}

type EmailSentMessage struct {
	To        string    `json:"to"`
	EmailType EmailType `json:"email_type"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
}

// Employee event topic constants
const (
	TopicEmployeeCreated = "user.employee-created"
	TopicEmployeeUpdated = "user.employee-updated"
)

// New topic constants
const (
	TopicCardTemporaryBlocked = "card.temporary-blocked"
	TopicVirtualCardCreated   = "card.virtual-card-created"
	TopicClientCreated        = "client.created"
	TopicClientUpdated        = "client.updated"
	TopicAccountCreated       = "account.created"
	TopicAccountStatusChanged = "account.status-changed"
	TopicCardCreated          = "card.created"
	TopicCardStatusChanged    = "card.status-changed"
	TopicPaymentCreated       = "transaction.payment-created"
	TopicPaymentCompleted     = "transaction.payment-completed"
	TopicPaymentFailed        = "transaction.payment-failed"
	TopicTransferCreated      = "transaction.transfer-created"
	TopicTransferCompleted    = "transaction.transfer-completed"
	TopicTransferFailed       = "transaction.transfer-failed"
	TopicSagaDeadLetter       = "transaction.saga-dead-letter"
	TopicLoanRequested        = "credit.loan-requested"
	TopicLoanApproved         = "credit.loan-approved"
	TopicLoanRejected         = "credit.loan-rejected"
	TopicInstallmentCollected = "credit.installment-collected"
	TopicInstallmentFailed    = "credit.installment-failed"
)

// Exchange service topics
const (
	TopicExchangeRatesUpdated = "exchange.rates-updated"
)

// ExchangeRatesUpdatedMessage is published after a successful rate sync.
// Other services can consume this to invalidate caches or trigger alerts.
type ExchangeRatesUpdatedMessage struct {
	CurrenciesUpdated []string `json:"currencies_updated"`
	UpdatedAt         string   `json:"updated_at"` // ISO-8601 timestamp of the sync
}

// New email type constants
const (
	EmailTypeAccountCreated      = EmailType("ACCOUNT_CREATED")
	EmailTypeCardVerification    = EmailType("CARD_VERIFICATION")
	EmailTypeCardStatusChanged   = EmailType("CARD_STATUS_CHANGED")
	EmailTypeLoanApproved        = EmailType("LOAN_APPROVED")
	EmailTypeLoanRejected        = EmailType("LOAN_REJECTED")
	EmailTypeInstallmentFailed   = EmailType("INSTALLMENT_FAILED")
	EmailTypeTransactionVerify   = EmailType("TRANSACTION_VERIFICATION")
	EmailTypePaymentConfirmation = EmailType("PAYMENT_CONFIRMATION")
)

type ClientCreatedMessage struct {
	ClientID  uint64 `json:"client_id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type AccountCreatedMessage struct {
	AccountNumber string `json:"account_number"`
	OwnerID       uint64 `json:"owner_id"`
	OwnerEmail    string `json:"owner_email"`
	AccountKind   string `json:"account_kind"`
	CurrencyCode  string `json:"currency_code"`
}

type CardCreatedMessage struct {
	CardID        uint64 `json:"card_id"`
	AccountNumber string `json:"account_number"`
	OwnerEmail    string `json:"owner_email"`
	CardBrand     string `json:"card_brand"`
}

type CardStatusChangedMessage struct {
	CardID            uint64 `json:"card_id"`
	AccountNumber     string `json:"account_number"`
	NewStatus         string `json:"new_status"`
	OwnerEmail        string `json:"owner_email"`
	AccountOwnerEmail string `json:"account_owner_email,omitempty"`
}

type PaymentCompletedMessage struct {
	PaymentID         uint64 `json:"payment_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	Amount            string `json:"amount"`
	Status            string `json:"status"`
}

type TransferCompletedMessage struct {
	TransferID        uint64 `json:"transfer_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	InitialAmount     string `json:"initial_amount"`
	FinalAmount       string `json:"final_amount"`
	ExchangeRate      string `json:"exchange_rate"`
}

// PaymentFailedMessage is published when a payment fails at any stage.
type PaymentFailedMessage struct {
	PaymentID         uint64 `json:"payment_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	Amount            string `json:"amount"`
	FailureReason     string `json:"failure_reason"`
}

// SagaDeadLetterMessage is published when a compensation step has failed
// MaxSagaRetries times and requires manual intervention.
type SagaDeadLetterMessage struct {
	SagaLogID       uint64 `json:"saga_log_id"`
	SagaID          string `json:"saga_id"`
	TransactionID   uint64 `json:"transaction_id"`
	TransactionType string `json:"transaction_type"` // "transfer" or "payment"
	StepName        string `json:"step_name"`
	AccountNumber   string `json:"account_number"`
	Amount          string `json:"amount"`
	RetryCount      int    `json:"retry_count"`
	LastError       string `json:"last_error"`
}

// TransferFailedMessage is published when a transfer fails at any stage.
type TransferFailedMessage struct {
	TransferID        uint64 `json:"transfer_id"`
	FromAccountNumber string `json:"from_account_number"`
	ToAccountNumber   string `json:"to_account_number"`
	Amount            string `json:"amount"`
	FailureReason     string `json:"failure_reason"`
}

type LoanStatusMessage struct {
	LoanRequestID uint64 `json:"loan_request_id"`
	ClientEmail   string `json:"client_email"`
	LoanType      string `json:"loan_type"`
	Amount        string `json:"amount"`
	Status        string `json:"status"`
}

type InstallmentResultMessage struct {
	LoanID        uint64 `json:"loan_id"`
	ClientEmail   string `json:"client_email"`
	Amount        string `json:"amount"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	RetryDeadline string `json:"retry_deadline,omitempty"`
}

// EmployeeCreatedMessage is published when an employee is created or updated.
type EmployeeCreatedMessage struct {
	EmployeeID int64    `json:"employee_id"`
	Email      string   `json:"email"`
	FirstName  string   `json:"first_name"`
	LastName   string   `json:"last_name"`
	Roles      []string `json:"roles"`
}

// Limit event topic constants
const (
	TopicEmployeeLimitsUpdated = "user.employee-limits-updated"
	TopicLimitTemplateCreated  = "user.limit-template-created"
	TopicLimitTemplateUpdated  = "user.limit-template-updated"
	TopicLimitTemplateDeleted  = "user.limit-template-deleted"
	TopicClientLimitsUpdated   = "client.limits-updated"
)

// EmployeeLimitsUpdatedMessage is published when an employee's limits are set or updated.
type EmployeeLimitsUpdatedMessage struct {
	EmployeeID int64  `json:"employee_id"`
	Action     string `json:"action"` // "set" or "template_applied"
}

// LimitTemplateMessage is published when a limit template is created, updated, or deleted.
type LimitTemplateMessage struct {
	TemplateID   int64  `json:"template_id"`
	TemplateName string `json:"template_name"`
	Action       string `json:"action"` // "created", "updated", "deleted"
}

// ClientLimitsUpdatedMessage is published when a client's limits are updated.
type ClientLimitsUpdatedMessage struct {
	ClientID      int64  `json:"client_id"`
	SetByEmployee int64  `json:"set_by_employee"`
	Action        string `json:"action"` // "set"
}

type CardTemporaryBlockedMessage struct {
	CardID    uint64 `json:"card_id"`
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason"`
}

type VirtualCardCreatedMessage struct {
	CardID        uint64 `json:"card_id"`
	AccountNumber string `json:"account_number"`
	UsageType     string `json:"usage_type"`
	MaxUses       int    `json:"max_uses"`
}

// Account event topic constants
const (
	TopicAccountNameUpdated      = "account.name-updated"
	TopicAccountLimitsUpdated    = "account.limits-updated"
	TopicMaintenanceFeeCharged   = "account.maintenance-charged"
	TopicSpendingReset           = "account.spending-reset"
)

// AccountNameUpdatedMessage is published when an account's name is changed.
type AccountNameUpdatedMessage struct {
	AccountID     uint64 `json:"account_id"`
	AccountNumber string `json:"account_number"`
	OldName       string `json:"old_name"`
	NewName       string `json:"new_name"`
}

// AccountLimitsUpdatedMessage is published when an account's daily/monthly limits are updated.
type AccountLimitsUpdatedMessage struct {
	AccountID     uint64 `json:"account_id"`
	AccountNumber string `json:"account_number"`
	DailyLimit    string `json:"daily_limit"`
	MonthlyLimit  string `json:"monthly_limit"`
}

// MaintenanceFeeChargedMessage is published when a maintenance fee is charged to an account.
type MaintenanceFeeChargedMessage struct {
	AccountNumber string `json:"account_number"`
	Amount        string `json:"amount"`
	CurrencyCode  string `json:"currency_code"`
}

// SpendingResetMessage is published when a periodic spending reset is performed.
type SpendingResetMessage struct {
	ResetType string `json:"reset_type"` // "daily" or "monthly"
	Count     int    `json:"count"`
}

// Credit event topic constants
const (
	TopicVariableRateAdjusted = "credit.variable-rate-adjusted"
	TopicLatePenaltyApplied   = "credit.late-penalty-applied"
)

// VariableRateAdjustedMessage is published when variable interest rates are recalculated.
type VariableRateAdjustedMessage struct {
	TierID        uint64 `json:"tier_id"`
	AffectedLoans int    `json:"affected_loans"`
	NewRate       string `json:"new_rate"`
}

// LatePenaltyAppliedMessage is published when a late payment penalty is applied to a loan.
type LatePenaltyAppliedMessage struct {
	LoanID  uint64 `json:"loan_id"`
	NewRate string `json:"new_rate"`
	Penalty string `json:"penalty"`
}

// Card request event topic constants
const (
	TopicCardRequestCreated  = "card.request-created"
	TopicCardRequestApproved = "card.request-approved"
	TopicCardRequestRejected = "card.request-rejected"
)

// CardRequestCreatedMessage is published when a client creates a card request.
type CardRequestCreatedMessage struct {
	RequestID     uint64 `json:"request_id"`
	ClientID      uint64 `json:"client_id"`
	AccountNumber string `json:"account_number"`
	CardBrand     string `json:"card_brand"`
}

// CardRequestApprovedMessage is published when an employee approves a card request.
type CardRequestApprovedMessage struct {
	RequestID  uint64 `json:"request_id"`
	CardID     uint64 `json:"card_id"`
	EmployeeID uint64 `json:"employee_id"`
}

// CardRequestRejectedMessage is published when an employee rejects a card request.
type CardRequestRejectedMessage struct {
	RequestID  uint64 `json:"request_id"`
	EmployeeID uint64 `json:"employee_id"`
	Reason     string `json:"reason"`
}

const (
	TopicAuthAccountStatusChanged = "auth.account-status-changed"
	TopicAuthDeadLetter           = "auth.dead-letter"
)

type AuthAccountStatusChangedMessage struct {
	PrincipalType string `json:"principal_type"`
	PrincipalID   int64  `json:"principal_id"`
	Status        string `json:"status"`
}
