package service

var rolePermissions = map[string][]string{
	"EmployeeBasic": {
		"clients.read",
		"accounts.create",
		"accounts.read",
		"cards.manage",
		"credits.manage",
	},
	"EmployeeAgent": {
		"clients.read",
		"accounts.create",
		"accounts.read",
		"cards.manage",
		"credits.manage",
		"securities.trade",
		"securities.read",
	},
	"EmployeeSupervisor": {
		"clients.read",
		"accounts.create",
		"accounts.read",
		"cards.manage",
		"credits.manage",
		"securities.trade",
		"securities.read",
		"agents.manage",
		"otc.manage",
		"funds.manage",
	},
	"EmployeeAdmin": {
		"clients.read",
		"accounts.create",
		"accounts.read",
		"cards.manage",
		"credits.manage",
		"securities.trade",
		"securities.read",
		"agents.manage",
		"otc.manage",
		"funds.manage",
		"employees.create",
		"employees.update",
		"employees.read",
		"employees.permissions",
	},
}

func ValidRole(role string) bool {
	_, ok := rolePermissions[role]
	return ok
}

func GetPermissions(role string) []string {
	perms, ok := rolePermissions[role]
	if !ok {
		return nil
	}
	result := make([]string, len(perms))
	copy(result, perms)
	return result
}
