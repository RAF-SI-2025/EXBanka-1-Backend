// user-service/internal/service/role_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidRole(t *testing.T) {
	tests := []struct {
		role  string
		valid bool
	}{
		{"EmployeeBasic", true},
		{"EmployeeAgent", true},
		{"EmployeeSupervisor", true},
		{"EmployeeAdmin", true},
		{"InvalidRole", false},
		{"", false},
		{"employeebasic", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			assert.Equal(t, tt.valid, ValidRole(tt.role))
		})
	}
}

func TestGetPermissions(t *testing.T) {
	tests := []struct {
		role        string
		expectPerms []string
	}{
		{"EmployeeBasic", []string{"clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage"}},
		{"EmployeeAdmin", []string{"clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage", "securities.trade", "securities.read", "agents.manage", "otc.manage", "funds.manage", "employees.create", "employees.update", "employees.read", "employees.permissions"}},
		{"InvalidRole", nil},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			perms := GetPermissions(tt.role)
			if tt.expectPerms == nil {
				assert.Nil(t, perms)
			} else {
				assert.Equal(t, tt.expectPerms, perms)
			}
		})
	}
}
