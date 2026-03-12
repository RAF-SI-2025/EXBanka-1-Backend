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
	assert.Contains(t, body, "http://localhost:3000/activate?token=abc123")
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
	assert.Contains(t, body, "http://localhost:3000/reset-password?token=reset123")
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
