package templates

// emailDefs are the code-defined email template types. Their default
// subject/body were migrated from the pre-3a sender.BuildEmail switch.
var emailDefs = []Definition{
	{
		Type: "ACTIVATION", Channel: "email",
		Description: "Sent when an employee or client account is created — carries the activation link.",
		Variables: []Variable{
			{"first_name", "Recipient's first name", "Ana"},
			{"link", "Account activation URL", "https://app.exbanka.rs/activate?token=..."},
		},
		DefaultSubject: "Activate Your EXBanka Account",
		DefaultBody: `<h2>Welcome, {{first_name}}!</h2>
<p>Your account has been created. Click the link below to activate your account and set your password:</p>
<p><a href="{{link}}" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Activate Account</a></p>
<p>Or copy this link into your browser:</p>
<p>{{link}}</p>
<p>This link expires in 24 hours.</p>`,
	},
	{
		Type: "PASSWORD_RESET", Channel: "email",
		Description: "Sent when a password reset is requested.",
		Variables: []Variable{
			{"link", "Password reset URL", "https://app.exbanka.rs/reset?token=..."},
		},
		DefaultSubject: "Password Reset Request",
		DefaultBody: `<h2>Password Reset</h2>
<p>Click the link below to reset your password:</p>
<p><a href="{{link}}" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link into your browser:</p>
<p>{{link}}</p>
<p>This link expires in 1 hour. If you did not request this, ignore this email.</p>`,
	},
	{
		Type: "CONFIRMATION", Channel: "email",
		Description: "Sent after an account is successfully activated.",
		Variables: []Variable{
			{"first_name", "Recipient's first name", "Ana"},
		},
		DefaultSubject: "Account Activated Successfully",
		DefaultBody: `<h2>Welcome aboard, {{first_name}}!</h2>
<p>Your EXBanka account has been successfully activated.</p>`,
	},
	{
		Type: "ACCOUNT_CREATED", Channel: "email",
		Description: "Sent when a new bank account is opened for a client.",
		Variables: []Variable{
			{"owner_name", "Account owner's name", "Ana Anić"},
			{"account_number", "The new account number", "265-1234567890123-45"},
			{"currency", "Account currency code", "RSD"},
			{"account_kind", "Account kind", "current"},
		},
		DefaultSubject: "Your New EXBanka Account Has Been Opened",
		DefaultBody: `<h2>Account Successfully Opened</h2>
<p>Dear {{owner_name}},</p>
<p>Your new bank account has been successfully created. Here are the details:</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{account_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Currency</strong></td><td style="padding:8px;border:1px solid #ddd;">{{currency}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{account_kind}}</td></tr>
</table>
<p>You can now use this account for transactions through your EXBanka online banking.</p>`,
	},
	{
		Type: "CARD_VERIFICATION", Channel: "email",
		Description: "Sent when a new card is issued — carries the CVV.",
		Variables: []Variable{
			{"card_last_four", "Last four digits of the card", "4242"},
			{"cvv", "Card security code", "123"},
		},
		DefaultSubject: "Your EXBanka Card Details",
		DefaultBody: `<h2>Card Verification Details</h2>
<p>Your card ending in <strong>{{card_last_four}}</strong> has been issued.</p>
<p>Your card security code (CVV): <strong>{{cvv}}</strong></p>
<p>Please keep this information confidential and do not share it with anyone.</p>
<p>If you did not request this card, please contact us immediately.</p>`,
	},
	{
		Type: "CARD_STATUS_CHANGED", Channel: "email",
		Description: "Sent when a card is blocked, unblocked, or deactivated.",
		Variables: []Variable{
			{"card_last_four", "Last four digits of the card", "4242"},
			{"account_number", "Account the card is linked to", "265-1234567890123-45"},
			{"new_status", "The card's new status", "blocked"},
		},
		DefaultSubject: "EXBanka Card Status Update",
		DefaultBody: `<h2>Card Status Changed</h2>
<p>The status of your card ending in <strong>{{card_last_four}}</strong> linked to account <strong>{{account_number}}</strong> has been updated.</p>
<p>New status: <strong>{{new_status}}</strong></p>
<p>If you did not request this change, please contact our support team immediately.</p>`,
	},
	{
		Type: "LOAN_APPROVED", Channel: "email",
		Description: "Sent when a loan request is approved.",
		Variables: []Variable{
			{"loan_number", "The loan number", "LN-000123"},
			{"loan_type", "Loan type", "cash"},
			{"amount", "Approved amount", "500000.00"},
			{"currency", "Loan currency code", "RSD"},
		},
		DefaultSubject: "Your EXBanka Loan Has Been Approved",
		DefaultBody: `<h2>Loan Approved</h2>
<p>Congratulations! Your loan application has been approved.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_type}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}} {{currency}}</td></tr>
</table>
<p>The funds will be disbursed to your designated account shortly.</p>`,
	},
	{
		Type: "LOAN_REJECTED", Channel: "email",
		Description: "Sent when a loan request is rejected.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
			{"reason", "Rejection reason", "Insufficient income"},
		},
		DefaultSubject: "EXBanka Loan Application Update",
		DefaultBody: `<h2>Loan Application Decision</h2>
<p>We regret to inform you that your loan application has not been approved at this time.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_type}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Requested Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Reason</strong></td><td style="padding:8px;border:1px solid #ddd;">{{reason}}</td></tr>
</table>
<p>You may reapply after addressing the above concerns. For assistance, please contact our support team.</p>`,
	},
	{
		Type: "INSTALLMENT_FAILED", Channel: "email",
		Description: "Sent when a scheduled loan installment collection fails.",
		Variables: []Variable{
			{"loan_number", "The loan number", "LN-000123"},
			{"amount", "Amount due", "12500.00"},
			{"currency", "Currency code", "RSD"},
			{"retry_deadline", "Deadline to ensure funds are available", "2026-06-01"},
		},
		DefaultSubject: "EXBanka Loan Installment Payment Failed",
		DefaultBody: `<h2>Installment Payment Failed</h2>
<p>We were unable to collect your scheduled loan installment payment.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount Due</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}} {{currency}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Retry Deadline</strong></td><td style="padding:8px;border:1px solid #ddd;">{{retry_deadline}}</td></tr>
</table>
<p>Please ensure sufficient funds are available in your account before the retry deadline to avoid late payment fees.</p>`,
	},
	{
		Type: "VERIFICATION_CODE", Channel: "email",
		Description: "Sent for a generic one-time verification code.",
		Variables: []Variable{
			{"code", "One-time verification code", "482913"},
			{"expires_in", "Human-readable expiry", "5 minutes"},
		},
		DefaultSubject: "EXBanka Verification Code",
		DefaultBody: `<h2>Verification Code</h2>
<p>Your one-time verification code is:</p>
<p style="font-size:32px;font-weight:bold;letter-spacing:8px;text-align:center;padding:20px;background:#f5f5f5;border-radius:4px;">{{code}}</p>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not initiate this request, please contact us immediately and do not share this code with anyone.</p>`,
	},
	{
		Type: "TRANSACTION_VERIFICATION", Channel: "email",
		Description: "Sent for a transaction-verification one-time code.",
		Variables: []Variable{
			{"verification_code", "One-time verification code", "482913"},
			{"expires_in", "Human-readable expiry", "5 minutes"},
		},
		DefaultSubject: "EXBanka Transaction Verification Code",
		DefaultBody: `<h2>Transaction Verification</h2>
<p>Your one-time verification code for the pending transaction is:</p>
<p style="font-size:32px;font-weight:bold;letter-spacing:8px;text-align:center;padding:20px;background:#f5f5f5;border-radius:4px;">{{verification_code}}</p>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not initiate this transaction, please contact us immediately and do not share this code with anyone.</p>`,
	},
	{
		Type: "PAYMENT_CONFIRMATION", Channel: "email",
		Description: "Sent to confirm a processed payment.",
		Variables: []Variable{
			{"status", "Payment status", "Completed"},
			{"from_account", "Source account number", "265-1111111111111-11"},
			{"to_account", "Destination account number", "265-2222222222222-22"},
			{"amount", "Payment amount", "10000.00 RSD"},
		},
		DefaultSubject: "EXBanka Payment Confirmation",
		DefaultBody: `<h2>Payment {{status}}</h2>
<p>Your payment has been processed.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>From Account</strong></td><td style="padding:8px;border:1px solid #ddd;">{{from_account}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>To Account</strong></td><td style="padding:8px;border:1px solid #ddd;">{{to_account}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Status</strong></td><td style="padding:8px;border:1px solid #ddd;">{{status}}</td></tr>
</table>
<p>Thank you for banking with EXBanka.</p>`,
	},
	{
		Type: "MOBILE_ACTIVATION", Channel: "email",
		Description: "Sent with the mobile-app activation code.",
		Variables: []Variable{
			{"code", "Mobile activation code", "482913"},
			{"expires_in", "Human-readable expiry", "15 minutes"},
		},
		DefaultSubject: "Your EXBanka Mobile App Activation Code",
		DefaultBody: `<html><body style="font-family:Arial,sans-serif;max-width:600px;margin:0 auto;">
<h2 style="color:#1a365d;">Mobile App Activation</h2>
<p>Use the following code to activate your EXBanka mobile app:</p>
<div style="background:#f0f4f8;padding:20px;text-align:center;border-radius:8px;margin:20px 0;">
	<span style="font-size:32px;font-weight:bold;letter-spacing:8px;color:#2d3748;">{{code}}</span>
</div>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not request this, please ignore this email.</p>
<p style="color:#718096;font-size:12px;">EXBanka Security Team</p>
</body></html>`,
	},
}
