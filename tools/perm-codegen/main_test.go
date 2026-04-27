package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestGenerate_GoldenOutput(t *testing.T) {
	yaml := []byte(`
permissions:
  - clients.read.all
  - clients.read.own
  - orders.place.own
default_roles:
  EmployeeBasic:
    grants: [clients.read.own]
  EmployeeAgent:
    inherits: [EmployeeBasic]
    grants: [clients.read.all, orders.place.own]
`)
	var buf bytes.Buffer
	if err := generate(yaml, &buf); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	for _, want := range []string{
		`var Catalog = []Permission{`,
		`"clients.read.all"`,
		`"clients.read.own"`,
		`"orders.place.own"`,
		`var Clients = struct {`,
		`var Orders = struct {`,
		`Read struct {`,
		`Place struct {`,
		`var DefaultRoles = map[string][]Permission{`,
		`"EmployeeBasic":`,
		`"EmployeeAgent":`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	yaml, err := os.ReadFile("../../contract/permissions/catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err := generate(yaml, &a); err != nil {
		t.Fatal(err)
	}
	if err := generate(yaml, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("codegen not deterministic")
	}
}
