package spec

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Rogsiel/medic/internal/units"
)

// SchemaProvider lets a type supply its own JSON Schema.
type SchemaProvider interface {
	JSONSchema(g *SchemaGen) map[string]any
}

var schemaProviderType = reflect.TypeOf((*SchemaProvider)(nil)).Elem()

// SchemaGen builds JSON Schema (draft 2020-12) from Go types.
type SchemaGen struct {
	Defs map[string]any
}

// NewSchemaGen returns a generator with the unit kinds predefined.
func NewSchemaGen() *SchemaGen {
	g := &SchemaGen{Defs: map[string]any{}}
	for _, d := range units.Defs {
		g.Defs[d.Kind] = map[string]any{
			"type":        "string",
			"pattern":     "(" + d.Pattern + ")|\\$\\{",
			"description": d.Description,
			"examples":    d.Examples,
		}
	}
	return g
}

// For returns the schema for t.
func (g *SchemaGen) For(t reflect.Type) map[string]any {
	if t.Implements(schemaProviderType) {
		return reflect.Zero(t).Interface().(SchemaProvider).JSONSchema(g)
	}
	if reflect.PointerTo(t).Implements(schemaProviderType) {
		return reflect.New(t).Interface().(SchemaProvider).JSONSchema(g)
	}
	if reflect.PointerTo(t).Implements(quantityType) {
		return map[string]any{"$ref": "#/$defs/" + reflect.New(t).Interface().(units.Quantity).Kind()}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return g.For(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"anyOf": []any{
			map[string]any{"type": "integer"},
			map[string]any{"type": "string", "pattern": "\\$\\{"},
		}}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": g.For(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.For(t.Elem())}
	case reflect.Struct:
		props, req := g.Props(t)
		return Object(props, req)
	}
	panic(fmt.Sprintf("spec: no schema for %s", t))
}

// Props returns the properties and required keys for one or more struct types.
func (g *SchemaGen) Props(types ...reflect.Type) (map[string]any, []string) {
	props := map[string]any{}
	var req []string
	for _, t := range types {
		for _, f := range Fields(t) {
			s := copyMap(g.For(f.Type))
			if f.Doc != "" {
				s["description"] = f.Doc
			}
			if len(f.Enum) > 0 {
				s["enum"] = f.Enum
			}
			props[f.Key] = s
			if f.Required {
				req = append(req, f.Key)
			}
		}
	}
	return props, req
}

// Object builds a closed object schema.
func Object(props map[string]any, required []string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		sort.Strings(required)
		s["required"] = required
	}
	return s
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// FieldDoc is one flattened row of reference documentation.
type FieldDoc struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
	Doc      string `json:"description"`
}

// Docs flattens the fields of v (a struct value holding defaults) into rows;
// nested option structs become dotted keys (expect.status).
func Docs(v reflect.Value, prefix string) []FieldDoc {
	var out []FieldDoc
	for _, f := range Fields(v.Type()) {
		fv := v.FieldByIndex(f.Index)
		key := prefix + f.Key
		ft := f.Type
		if ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct && !isLeaf(ft.Elem()) {
			ft = ft.Elem()
			if fv.IsNil() {
				fv = reflect.New(ft).Elem()
			} else {
				fv = fv.Elem()
			}
		}
		if ft.Kind() == reflect.Struct && !isLeaf(ft) {
			out = append(out, FieldDoc{Key: key, Type: "mapping", Required: f.Required, Doc: f.Doc})
			out = append(out, Docs(fv, key+".")...)
			continue
		}
		typ := TypeName(f.Type)
		doc := f.Doc
		if len(f.Enum) > 0 {
			typ = "one of: " + strings.Join(f.Enum, ", ")
		}
		if f.Threshold != "" {
			word := "above"
			if f.Threshold == "below" {
				word = "below"
			}
			doc = strings.TrimSpace(doc + " Triggers when the value is " + word + " the threshold.")
		}
		out = append(out, FieldDoc{Key: key, Type: typ, Required: f.Required, Default: FormatDefault(fv), Doc: doc})
	}
	return out
}

// isLeaf reports whether a struct type is a single value (quantity,
// threshold, or custom) rather than a group of options.
func isLeaf(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(quantityType) {
		return true
	}
	_, ok := reflect.New(t).Interface().(interface{ TypeName() string })
	return ok
}

// FormatDefault renders a default value for docs; "" means no default.
func FormatDefault(v reflect.Value) string {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.CanAddr() {
		if q, ok := v.Addr().Interface().(units.Quantity); ok {
			if v.IsZero() {
				return ""
			}
			return q.String()
		}
	}
	if s, ok := v.Interface().(fmt.Stringer); ok {
		if v.IsZero() {
			return ""
		}
		return s.String()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return fmt.Sprint(v.Bool())
	case reflect.Int, reflect.Int64, reflect.Int32:
		return fmt.Sprint(v.Int())
	case reflect.Slice:
		if v.Len() == 0 {
			return ""
		}
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = FormatDefault(v.Index(i))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return ""
}
