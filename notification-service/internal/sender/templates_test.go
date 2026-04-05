// notification-service/internal/sender/templates_test.go
package sender

import (
	"testing"

	"github.com/stretchr/testify/assert"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestBuildEmail_Activation(t *testing.T) {
	data := map[string]string{
		"first_name": "John",
		"token":      "abc123",
		"link":       "http://localhost:5173/activate?token=abc123",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeActivation, data)

	assert.Equal(t, "Activate Your EXBanka Account", subject)
	assert.Contains(t, body, "John")
	assert.Contains(t, body, "http://localhost:5173/activate?token=abc123")
	assert.Contains(t, body, "Activate Account")
	assert.Contains(t, body, "24 hours")
}

func TestBuildEmail_PasswordReset(t *testing.T) {
	data := map[string]string{
		"token": "reset123",
		"link":  "http://localhost:5173/reset-password?token=reset123",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypePasswordReset, data)

	assert.Equal(t, "Password Reset Request", subject)
	assert.Contains(t, body, "http://localhost:5173/reset-password?token=reset123")
	assert.Contains(t, body, "Reset Password")
	assert.Contains(t, body, "1 hour")
}

func TestBuildEmail_Confirmation(t *testing.T) {
	data := map[string]string{"first_name": "Jane"}
	subject, body := BuildEmail(kafkamsg.EmailTypeConfirmation, data)

	assert.Equal(t, "Account Activated Successfully", subject)
	assert.Contains(t, body, "Jane")
	assert.Contains(t, body, "activated")
}

func TestBuildEmail_UnknownType(t *testing.T) {
	subject, body := BuildEmail("UNKNOWN", nil)

	assert.Equal(t, "EXBanka Notification", subject)
	assert.Contains(t, body, "notification")
}

func TestBuildEmail_VerificationCode(t *testing.T) {
	data := map[string]string{
		"code":       "847291",
		"expires_in": "5 minutes",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeVerificationCode, data)

	assert.Equal(t, "EXBanka Verification Code", subject)
	assert.Contains(t, body, "847291")
	assert.Contains(t, body, "5 minutes")
	// Code should be displayed prominently
	assert.Contains(t, body, "font-size:32px")
}

func TestBuildEmail_MobileActivation(t *testing.T) {
	data := map[string]string{
		"code":       "553812",
		"expires_in": "15 minutes",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeMobileActivation, data)

	assert.Equal(t, "Your EXBanka Mobile App Activation Code", subject)
	assert.Contains(t, body, "553812")
	assert.Contains(t, body, "15 minutes")
	assert.Contains(t, body, "Mobile App Activation")
}

func TestBuildEmail_AccountCreated(t *testing.T) {
	data := map[string]string{
		"account_number": "265000000000000001",
		"currency":       "RSD",
		"account_kind":   "current",
		"owner_name":     "Marko Markovic",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeAccountCreated, data)

	assert.Equal(t, "Your New EXBanka Account Has Been Opened", subject)
	assert.Contains(t, body, "Marko Markovic")
	assert.Contains(t, body, "265000000000000001")
	assert.Contains(t, body, "RSD")
	assert.Contains(t, body, "current")
}

func TestBuildEmail_CardVerification(t *testing.T) {
	data := map[string]string{
		"card_last_four": "4242",
		"cvv":            "737",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeCardVerification, data)

	assert.Equal(t, "Your EXBanka Card Details", subject)
	assert.Contains(t, body, "4242")
	assert.Contains(t, body, "737")
}

func TestBuildEmail_CardStatusChanged(t *testing.T) {
	data := map[string]string{
		"card_last_four": "1234",
		"new_status":     "blocked",
		"account_number": "265000000000000001",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeCardStatusChanged, data)

	assert.Equal(t, "EXBanka Card Status Update", subject)
	assert.Contains(t, body, "1234")
	assert.Contains(t, body, "blocked")
	assert.Contains(t, body, "265000000000000001")
}

func TestBuildEmail_LoanApproved(t *testing.T) {
	data := map[string]string{
		"loan_type":   "cash",
		"amount":      "500000",
		"loan_number": "LN-0001",
		"currency":    "RSD",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeLoanApproved, data)

	assert.Equal(t, "Your EXBanka Loan Has Been Approved", subject)
	assert.Contains(t, body, "LN-0001")
	assert.Contains(t, body, "500000")
	assert.Contains(t, body, "RSD")
}

func TestBuildEmail_LoanRejected(t *testing.T) {
	data := map[string]string{
		"loan_type": "housing",
		"amount":    "1000000",
		"reason":    "Insufficient credit score",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeLoanRejected, data)

	assert.Equal(t, "EXBanka Loan Application Update", subject)
	assert.Contains(t, body, "housing")
	assert.Contains(t, body, "Insufficient credit score")
}

func TestBuildEmail_InstallmentFailed(t *testing.T) {
	data := map[string]string{
		"loan_number":    "LN-0001",
		"amount":         "12500.00",
		"currency":       "RSD",
		"retry_deadline": "2026-04-10",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeInstallmentFailed, data)

	assert.Equal(t, "EXBanka Loan Installment Payment Failed", subject)
	assert.Contains(t, body, "LN-0001")
	assert.Contains(t, body, "12500.00")
	assert.Contains(t, body, "2026-04-10")
}

func TestBuildEmail_TransactionVerify(t *testing.T) {
	data := map[string]string{
		"verification_code": "192837",
		"expires_in":        "10 minutes",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypeTransactionVerify, data)

	assert.Equal(t, "EXBanka Transaction Verification Code", subject)
	assert.Contains(t, body, "192837")
	assert.Contains(t, body, "10 minutes")
}

func TestBuildEmail_PaymentConfirmation(t *testing.T) {
	data := map[string]string{
		"from_account": "265000000000000001",
		"to_account":   "265000000000000002",
		"amount":       "5000 RSD",
		"status":       "completed",
	}
	subject, body := BuildEmail(kafkamsg.EmailTypePaymentConfirmation, data)

	assert.Equal(t, "EXBanka Payment Confirmation", subject)
	assert.Contains(t, body, "265000000000000001")
	assert.Contains(t, body, "265000000000000002")
	assert.Contains(t, body, "5000 RSD")
	assert.Contains(t, body, "completed")
}
