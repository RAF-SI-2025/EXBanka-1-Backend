package permissions_test

import (
	"os"
	"strings"
	"testing"

	"github.com/exbanka/contract/permissions"
)

func TestLoadCatalog_RejectsBadName(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read    # only 2 segments
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "must have exactly 3 segments") {
		t.Errorf("expected 3-segment error, got %v", err)
	}
}

func TestLoadCatalog_RejectsHyphen(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - bank-accounts.manage.any
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "snake_case") {
		t.Errorf("expected snake_case error, got %v", err)
	}
}

func TestLoadCatalog_RejectsDuplicate(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.all
default_roles: {}
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestLoadCatalog_RejectsCycleInInherits(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions: [clients.read.all]
default_roles:
  A: { inherits: [B], grants: [] }
  B: { inherits: [A], grants: [] }
`))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected cycle error, got %v", err)
	}
}

func TestLoadCatalog_RejectsGrantNotInCatalog(t *testing.T) {
	_, err := permissions.ParseYAML([]byte(`
permissions: [clients.read.all]
default_roles:
  A: { grants: [does.not.exist] }
`))
	if err == nil || !strings.Contains(err.Error(), "not in catalog") {
		t.Errorf("expected not-in-catalog error, got %v", err)
	}
}

func TestLoadCatalog_FlattensInheritance(t *testing.T) {
	cat, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.own
default_roles:
  A: { grants: [clients.read.own] }
  B: { inherits: [A], grants: [clients.read.all] }
`))
	if err != nil {
		t.Fatal(err)
	}
	got := cat.Roles["B"]
	if len(got) != 2 {
		t.Errorf("expected 2 perms after flatten, got %d", len(got))
	}
}

func TestLoadCatalog_StarExpandsToAll(t *testing.T) {
	cat, err := permissions.ParseYAML([]byte(`
permissions:
  - clients.read.all
  - clients.read.own
default_roles:
  Admin: { grants: ["*"] }
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Roles["Admin"]) != 2 {
		t.Errorf("'*' should expand to 2, got %d", len(cat.Roles["Admin"]))
	}
}

func TestActualCatalog_LoadsCleanly(t *testing.T) {
	data, err := os.ReadFile("catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := permissions.ParseYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	// NOTE: Plan title claims ~140 permissions, but the catalog YAML in the
	// plan body lists 69. We assert >= 60 to guard against accidental gutting
	// of the catalog while matching the plan's actual content.
	if len(cat.All) < 60 {
		t.Errorf("expected at least 60 permissions, got %d", len(cat.All))
	}
	for _, role := range []string{"EmployeeBasic", "EmployeeAgent", "EmployeeSupervisor", "EmployeeAdmin"} {
		if _, ok := cat.Roles[role]; !ok {
			t.Errorf("missing role %q", role)
		}
	}
}
