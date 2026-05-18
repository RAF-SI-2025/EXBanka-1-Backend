// Package templates holds the code-defined notification template registry.
// A template type's set of variables is fixed by the publishing service —
// admins customize the subject/body text, they do not invent types or
// variables. The registry seeds the default text; the DB override table
// (model.NotificationTemplate) holds admin customizations.
package templates

// Variable is one substitutable field a template type supports.
type Variable struct {
	Name        string
	Description string
	Example     string
}

// Definition is one notification template type.
type Definition struct {
	Type           string
	Channel        string // "email" | "push"
	Description    string
	Variables      []Variable
	DefaultSubject string
	DefaultBody    string
}

// registry is the full set of known template types, assembled from the
// per-channel definition slices in registry_email.go and registry_push.go.
var registry = func() []Definition {
	all := make([]Definition, 0, len(emailDefs)+len(pushDefs))
	all = append(all, emailDefs...)
	all = append(all, pushDefs...)
	return all
}()

// All returns every registered template definition. If channel is non-empty,
// only definitions for that channel are returned.
func All(channel string) []Definition {
	if channel == "" {
		out := make([]Definition, len(registry))
		copy(out, registry)
		return out
	}
	var out []Definition
	for _, d := range registry {
		if d.Channel == channel {
			out = append(out, d)
		}
	}
	return out
}

// Get returns the definition for a (type, channel) pair.
func Get(typ, channel string) (Definition, bool) {
	for _, d := range registry {
		if d.Type == typ && d.Channel == channel {
			return d, true
		}
	}
	return Definition{}, false
}

// KnownVars returns the set of variable names a (type, channel) supports.
func KnownVars(typ, channel string) (map[string]bool, bool) {
	d, ok := Get(typ, channel)
	if !ok {
		return nil, false
	}
	set := make(map[string]bool, len(d.Variables))
	for _, v := range d.Variables {
		set[v.Name] = true
	}
	return set, true
}
