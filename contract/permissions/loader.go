// Package permissions defines the typed permission catalog used across the
// system. The catalog is loaded from a YAML source of truth at build time
// (codegen) and at runtime for validation. Admins can grant or revoke
// role↔permission mappings via the API, but cannot add or remove permissions
// from the catalog without a code change.
package permissions

import (
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// Permission is a strongly-typed permission identifier of the form
// "<resource>.<verb>.<scope>".
type Permission string

// String returns the underlying string form of the permission.
func (p Permission) String() string { return string(p) }

// Catalog is the parsed and validated permission catalog.
type Catalog struct {
	All   []Permission            // every permission, in catalog order
	Set   map[Permission]struct{} // membership set
	Roles map[string][]Permission // flattened role → permissions
}

// IsValid reports whether p is part of the catalog.
func (c *Catalog) IsValid(p Permission) bool {
	_, ok := c.Set[p]
	return ok
}

type yamlDoc struct {
	Permissions  []string            `yaml:"permissions"`
	DefaultRoles map[string]yamlRole `yaml:"default_roles"`
}

type yamlRole struct {
	Inherits []string `yaml:"inherits"`
	Grants   []string `yaml:"grants"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// ParseYAML parses the catalog YAML, validates names, detects duplicates,
// resolves role inheritance, validates grants against the catalog, and
// returns a flattened Catalog.
func ParseYAML(data []byte) (*Catalog, error) {
	var doc yamlDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	cat := &Catalog{
		All:   make([]Permission, 0, len(doc.Permissions)),
		Set:   make(map[Permission]struct{}),
		Roles: make(map[string][]Permission),
	}

	// Validate permission names + uniqueness.
	for _, raw := range doc.Permissions {
		if !nameRe.MatchString(raw) {
			parts := splitOrEmpty(raw, ".")
			if len(parts) != 3 {
				return nil, fmt.Errorf("permission %q must have exactly 3 segments separated by '.'", raw)
			}
			return nil, fmt.Errorf("permission %q invalid: must be snake_case lowercase letters/digits/underscore, three segments", raw)
		}
		p := Permission(raw)
		if _, dup := cat.Set[p]; dup {
			return nil, fmt.Errorf("duplicate permission: %q", raw)
		}
		cat.Set[p] = struct{}{}
		cat.All = append(cat.All, p)
	}

	// Resolve roles with cycle + grant validation.
	visited := make(map[string]bool)
	stack := make(map[string]bool)
	// Deterministic iteration order for stable error messages.
	roleNames := make([]string, 0, len(doc.DefaultRoles))
	for name := range doc.DefaultRoles {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)
	for _, name := range roleNames {
		if err := flattenRole(name, doc.DefaultRoles, cat, visited, stack); err != nil {
			return nil, err
		}
	}

	return cat, nil
}

func flattenRole(name string, all map[string]yamlRole, cat *Catalog,
	visited, stack map[string]bool) error {

	if stack[name] {
		return fmt.Errorf("cycle in role inherits at %q", name)
	}
	if visited[name] {
		return nil
	}
	role, ok := all[name]
	if !ok {
		return fmt.Errorf("role %q referenced but not defined", name)
	}
	stack[name] = true
	defer delete(stack, name)

	out := map[Permission]struct{}{}

	for _, parent := range role.Inherits {
		if err := flattenRole(parent, all, cat, visited, stack); err != nil {
			return err
		}
		for _, p := range cat.Roles[parent] {
			out[p] = struct{}{}
		}
	}

	for _, raw := range role.Grants {
		if raw == "*" {
			for _, p := range cat.All {
				out[p] = struct{}{}
			}
			continue
		}
		p := Permission(raw)
		if !cat.IsValid(p) {
			return fmt.Errorf("role %q grants %q which is not in catalog", name, raw)
		}
		out[p] = struct{}{}
	}

	flat := make([]Permission, 0, len(out))
	for p := range out {
		flat = append(flat, p)
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i] < flat[j] })
	cat.Roles[name] = flat
	visited[name] = true
	return nil
}

func splitOrEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, r := range s {
		if string(r) == sep {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
