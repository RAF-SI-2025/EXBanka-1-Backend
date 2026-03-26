package sender

import (
	"fmt"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func BuildEmail(emailType kafkamsg.EmailType, data map[string]string) (subject, body string) {
	switch emailType {
	case kafkamsg.EmailTypeActivation:
		subject = "Activate Your EXBanka Account"
		name := data["first_name"]
		link := data["link"]
		body = fmt.Sprintf(`<h2>Welcome, %s!</h2>
<p>Your account has been created. Click the link below to activate your account and set your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Activate Account</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 24 hours.</p>`, name, link, link)

	case kafkamsg.EmailTypePasswordReset:
		link := data["link"]
		subject = "Password Reset Request"
		body = fmt.Sprintf(`<h2>Password Reset</h2>
<p>Click the link below to reset your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 1 hour. If you did not request this, ignore this email.</p>`, link, link)

	case kafkamsg.EmailTypeConfirmation:
		name := data["first_name"]
		subject = "Account Activated Successfully"
		body = fmt.Sprintf(`<h2>Welcome aboard, %s!</h2>
<p>Your EXBanka account has been successfully activated.</p>`, name)

	case kafkamsg.EmailTypeAccountCreated:
		accountNumber := data["account_number"]
		currency := data["currency"]
		accountKind := data["account_kind"]
		ownerName := data["owner_name"]
		subject = "Your New EXBanka Account Has Been Opened"
		body = fmt.Sprintf(`<h2>Account Successfully Opened</h2>
<p>Dear %s,</p>
<p>Your new bank account has been successfully created. Here are the details:</p>
<table style="border-collapse:collapse;width:100%%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Number</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Currency</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Type</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
</table>
<p>You can now use this account for transactions through your EXBanka online banking.</p>`, ownerName, accountNumber, currency, accountKind)

	case kafkamsg.EmailTypeCardVerification:
		cardLastFour := data["card_last_four"]
		cvv := data["cvv"]
		subject = "Your EXBanka Card Details"
		body = fmt.Sprintf(`<h2>Card Verification Details</h2>
<p>Your card ending in <strong>%s</strong> has been issued.</p>
<p>Your card security code (CVV): <strong>%s</strong></p>
<p>Please keep this information confidential and do not share it with anyone.</p>
<p>If you did not request this card, please contact us immediately.</p>`, cardLastFour, cvv)

	case kafkamsg.EmailTypeCardStatusChanged:
		cardLastFour := data["card_last_four"]
		newStatus := data["new_status"]
		accountNumber := data["account_number"]
		subject = "EXBanka Card Status Update"
		body = fmt.Sprintf(`<h2>Card Status Changed</h2>
<p>The status of your card ending in <strong>%s</strong> linked to account <strong>%s</strong> has been updated.</p>
<p>New status: <strong>%s</strong></p>
<p>If you did not request this change, please contact our support team immediately.</p>`, cardLastFour, accountNumber, newStatus)

	case kafkamsg.EmailTypeLoanApproved:
		loanType := data["loan_type"]
		amount := data["amount"]
		loanNumber := data["loan_number"]
		currency := data["currency"]
		subject = "Your EXBanka Loan Has Been Approved"
		body = fmt.Sprintf(`<h2>Loan Approved</h2>
<p>Congratulations! Your loan application has been approved.</p>
<table style="border-collapse:collapse;width:100%%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">%s %s</td></tr>
</table>
<p>The funds will be disbursed to your designated account shortly.</p>`, loanNumber, loanType, amount, currency)

	case kafkamsg.EmailTypeLoanRejected:
		loanType := data["loan_type"]
		amount := data["amount"]
		reason := data["reason"]
		subject = "EXBanka Loan Application Update"
		body = fmt.Sprintf(`<h2>Loan Application Decision</h2>
<p>We regret to inform you that your loan application has not been approved at this time.</p>
<table style="border-collapse:collapse;width:100%%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Requested Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Reason</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
</table>
<p>You may reapply after addressing the above concerns. For assistance, please contact our support team.</p>`, loanType, amount, reason)

	case kafkamsg.EmailTypeInstallmentFailed:
		loanNumber := data["loan_number"]
		amount := data["amount"]
		currency := data["currency"]
		retryDeadline := data["retry_deadline"]
		subject = "EXBanka Loan Installment Payment Failed"
		body = fmt.Sprintf(`<h2>Installment Payment Failed</h2>
<p>We were unable to collect your scheduled loan installment payment.</p>
<table style="border-collapse:collapse;width:100%%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount Due</strong></td><td style="padding:8px;border:1px solid #ddd;">%s %s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Retry Deadline</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
</table>
<p>Please ensure sufficient funds are available in your account before the retry deadline to avoid late payment fees.</p>`, loanNumber, amount, currency, retryDeadline)

	case kafkamsg.EmailTypeTransactionVerify:
		verificationCode := data["verification_code"]
		expiresIn := data["expires_in"]
		subject = "EXBanka Transaction Verification Code"
		body = fmt.Sprintf(`<h2>Transaction Verification</h2>
<p>Your one-time verification code for the pending transaction is:</p>
<p style="font-size:32px;font-weight:bold;letter-spacing:8px;text-align:center;padding:20px;background:#f5f5f5;border-radius:4px;">%s</p>
<p>This code expires in <strong>%s</strong>.</p>
<p>If you did not initiate this transaction, please contact us immediately and do not share this code with anyone.</p>`, verificationCode, expiresIn)

	case kafkamsg.EmailTypePaymentConfirmation:
		fromAccount := data["from_account"]
		toAccount := data["to_account"]
		amount := data["amount"]
		txStatus := data["status"]
		subject = "EXBanka Payment Confirmation"
		body = fmt.Sprintf(`<h2>Payment %s</h2>
<p>Your payment has been processed.</p>
<table style="border-collapse:collapse;width:100%%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>From Account</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>To Account</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Status</strong></td><td style="padding:8px;border:1px solid #ddd;">%s</td></tr>
</table>
<p>Thank you for banking with EXBanka.</p>`, txStatus, fromAccount, toAccount, amount, txStatus)

	default:
		subject = "EXBanka Notification"
		body = "<p>You have a new notification from EXBanka.</p>"
	}
	return subject, body
}
