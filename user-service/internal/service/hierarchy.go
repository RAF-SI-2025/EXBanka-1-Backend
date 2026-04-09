package service

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/user-service/internal/model"
)

// roleRanks defines the hierarchy: higher number = higher rank.
var roleRanks = map[string]int{
	"EmployeeBasic":      1,
	"EmployeeAgent":      2,
	"EmployeeSupervisor": 3,
	"EmployeeAdmin":      4,
}

// maxRoleRank returns the highest rank from a list of roles.
// Returns 0 if no recognized roles are present.
func maxRoleRank(roles []model.Role) int {
	max := 0
	for _, r := range roles {
		if rank, ok := roleRanks[r.Name]; ok && rank > max {
			max = rank
		}
	}
	return max
}

// HierarchyEmpRepo is the minimal interface needed for hierarchy checks.
type HierarchyEmpRepo interface {
	GetByIDWithRoles(id int64) (*model.Employee, error)
}

// checkHierarchy verifies that callerID has strictly higher role rank than
// targetEmployeeID. Returns a gRPC PermissionDenied error on violation.
// Also blocks self-modification (callerID == targetEmployeeID).
func checkHierarchy(empRepo HierarchyEmpRepo, callerID, targetEmployeeID int64) error {
	if callerID == 0 {
		// System-initiated change (no caller metadata) -- allow.
		return nil
	}
	if callerID == targetEmployeeID {
		return status.Error(codes.PermissionDenied, "cannot modify own limits")
	}

	caller, err := empRepo.GetByIDWithRoles(callerID)
	if err != nil {
		return status.Error(codes.Internal, "failed to load caller employee")
	}

	target, err := empRepo.GetByIDWithRoles(targetEmployeeID)
	if err != nil {
		return status.Error(codes.Internal, "failed to load target employee")
	}

	callerRank := maxRoleRank(caller.Roles)
	targetRank := maxRoleRank(target.Roles)

	if callerRank <= targetRank {
		return status.Error(codes.PermissionDenied,
			"insufficient rank to modify this employee's limits")
	}
	return nil
}
