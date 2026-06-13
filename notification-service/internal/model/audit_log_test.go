package model

import "testing"

func TestAuditLogTableNames(t *testing.T) {
	if got := (AdminAuditLog{}).TableName(); got != "admin_audit_logs" {
		t.Errorf("AdminAuditLog.TableName() = %q, want admin_audit_logs", got)
	}
	if got := (BusinessAuditLog{}).TableName(); got != "business_audit_logs" {
		t.Errorf("BusinessAuditLog.TableName() = %q, want business_audit_logs", got)
	}
}
