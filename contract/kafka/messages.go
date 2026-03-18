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
	TopicClientCreated        = "client.created"
	TopicClientUpdated        = "client.updated"
	TopicAccountCreated       = "account.created"
	TopicAccountStatusChanged = "account.status-changed"
	TopicCardCreated          = "card.created"
	TopicCardStatusChanged    = "card.status-changed"
	TopicPaymentCreated       = "transaction.payment-created"
	TopicPaymentCompleted     = "transaction.payment-completed"
	TopicTransferCreated      = "transaction.transfer-created"
	TopicTransferCompleted    = "transaction.transfer-completed"
	TopicLoanRequested        = "credit.loan-requested"
	TopicLoanApproved         = "credit.loan-approved"
	TopicLoanRejected         = "credit.loan-rejected"
	TopicInstallmentCollected = "credit.installment-collected"
	TopicInstallmentFailed    = "credit.installment-failed"
)

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
	EmployeeID int64  `json:"employee_id"`
	Email      string `json:"email"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Role       string `json:"role"`
}
