package curate

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Priorities holds user-defined priority descriptions that guide the curator
// when deciding which observations to protect or boost.
type Priorities struct {
	// Namespaced maps a namespace name to its list of priority descriptions.
	Namespaced map[string][]string
	// Global holds priorities from the "_global" key that apply to all namespaces.
	Global []string
}

const globalKey = "_global"

// LoadPriorities reads a YAML file at path and returns the parsed Priorities.
// If the file does not exist, an empty Priorities is returned with no error —
// this is expected behaviour when the user has not yet created the file.
//
// The YAML file must be a flat mapping of string → []string, for example:
//
//	neurox:
//	  - "Architecture decisions about Buffer→Working→Core model"
//	_global:
//	  - "My name and personal preferences"
func LoadPriorities(path string) (Priorities, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Priorities{}, nil
		}
		return Priorities{}, fmt.Errorf("read priorities file %s: %w", path, err)
	}

	var raw map[string][]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Priorities{}, fmt.Errorf("parse priorities file %s: %w", path, err)
	}

	p := Priorities{
		Namespaced: make(map[string][]string),
	}
	for key, entries := range raw {
		if key == globalKey {
			p.Global = entries
		} else {
			p.Namespaced[key] = entries
		}
	}
	return p, nil
}

// ForNamespace returns the combined list of priorities for the given namespace:
// namespace-specific entries first, then global entries. The returned slice may
// be empty but is never nil.
func (p Priorities) ForNamespace(ns string) []string {
	specific := p.Namespaced[ns]
	total := len(specific) + len(p.Global)
	if total == 0 {
		return []string{}
	}
	result := make([]string, 0, total)
	result = append(result, specific...)
	result = append(result, p.Global...)
	return result
}
