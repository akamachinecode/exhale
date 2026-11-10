// Package toolschema turns a use case of the op catalog into the JSON
// Schema of a model tool. The MCP server and the in-app assistant share it,
// so both describe every tool the same way (ADR 0018).
package toolschema

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
)

// Input returns the JSON Schema of a use case's input, with the constraints
// declared as struct tags for the REST API copied in.
func Input(b app.Binding) (*jsonschema.Schema, error) {
	in, err := jsonschema.ForType(b.In, &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("tool %s: input schema: %w", b.Name, err)
	}
	if in.Properties == nil {
		in.Properties = map[string]*jsonschema.Schema{}
	}
	applyTags(in, b.In)
	return in, nil
}

// Output returns the JSON Schema of a use case's result.
func Output(b app.Binding) (*jsonschema.Schema, error) {
	out, err := jsonschema.ForType(b.Out, &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("tool %s: output schema: %w", b.Name, err)
	}
	return out, nil
}

// HasField reports whether the input of b has a JSON field called name
// (looking into embedded structs).
func HasField(b app.Binding, name string) bool { return hasField(b.In, name) }

func hasField(t reflect.Type, name string) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			if hasField(f.Type, name) {
				return true
			}
			continue
		}
		if n, _, _ := strings.Cut(f.Tag.Get("json"), ","); n == name {
			return true
		}
	}
	return false
}

// applyTags copies the constraints declared as struct tags for the REST API
// (enum, minimum, maximum, maxLength) into the tool's input schema, so an
// agent sees the allowed values instead of guessing them.
func applyTags(schema *jsonschema.Schema, t reflect.Type) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			applyTags(schema, f.Type)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		prop := schema.Properties[name]
		if name == "" || name == "-" || prop == nil {
			continue
		}
		if v := f.Tag.Get("enum"); v != "" {
			prop.Enum = nil
			for _, e := range strings.Split(v, ",") {
				prop.Enum = append(prop.Enum, e)
			}
		}
		if v, err := strconv.ParseFloat(f.Tag.Get("minimum"), 64); err == nil {
			prop.Minimum = &v
		}
		if v, err := strconv.ParseFloat(f.Tag.Get("maximum"), 64); err == nil {
			prop.Maximum = &v
		}
		if v, err := strconv.Atoi(f.Tag.Get("maxLength")); err == nil {
			prop.MaxLength = &v
		}
	}
}

// ErrorText is what a model is told when a tool fails: the error, plus a
// hint that helps it recover instead of repeating the call.
func ErrorText(err error) string {
	hint := ""
	switch {
	case errors.Is(err, domain.ErrNotFound):
		hint = " (check the id with list_subscriptions or get_context)"
	case errors.Is(err, domain.ErrConflict):
		hint = " (the data changed meanwhile; read it again and retry)"
	case errors.Is(err, app.ErrForbidden):
		hint = " (this caller may not do that)"
	}
	return err.Error() + hint
}
