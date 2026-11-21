package spec

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Rogsiel/medic/internal/units"
)

// NodeDecoder lets a type take over decoding of its own node.
type NodeDecoder interface {
	DecodeNode(d *Decoder, n *Node, path string)
}

// Validator is called after a struct has been decoded.
type Validator interface {
	Validate() error
}

// DirectionChecker validates warn/fail ordering for a field tagged
// `threshold:"above"` or `threshold:"below"`.
type DirectionChecker interface {
	CheckDirection(dir string) error
}

var (
	quantityType    = reflect.TypeOf((*units.Quantity)(nil)).Elem()
	nodeDecoderType = reflect.TypeOf((*NodeDecoder)(nil)).Elem()
)

// Decoder decodes Nodes into Go values, collecting every error.
type Decoder struct {
	// Interp expands ${VAR} references in scalar text. Nil means no expansion.
	Interp func(s string) (string, error)
	Errs   Errors
}

// Errorf records an error at pos.
func (d *Decoder) Errorf(p Pos, path, format string, args ...any) {
	d.Errs = append(d.Errs, Error{Pos: p, Path: path, Msg: fmt.Sprintf(format, args...)})
}

// Err returns the collected errors (sorted) or nil.
func (d *Decoder) Err() error {
	if len(d.Errs) == 0 {
		return nil
	}
	d.Errs.Sort()
	return d.Errs
}

// Scalar returns the interpolated text of a scalar node.
func (d *Decoder) Scalar(n *Node, path string) (string, bool) {
	if n.Kind != Scalar {
		d.Errorf(n.Pos, path, "expected a %s, got a %s", "scalar value", n.Kind)
		return "", false
	}
	if d.Interp == nil {
		return n.Value, true
	}
	s, err := d.Interp(n.Value)
	if err != nil {
		d.Errorf(n.Pos, path, "%v", err)
		return "", false
	}
	return s, true
}

// Decode decodes n into the addressable value v.
func (d *Decoder) Decode(n *Node, path string, v reflect.Value) {
	t := v.Type()

	if reflect.PointerTo(t).Implements(nodeDecoderType) {
		v.Addr().Interface().(NodeDecoder).DecodeNode(d, n, path)
		return
	}
	if n.Kind == Null {
		d.Errorf(n.Pos, path, "missing value (expected %s)", TypeName(t))
		return
	}
	if reflect.PointerTo(t).Implements(quantityType) {
		s, ok := d.scalarOf(n, path, t)
		if !ok {
			return
		}
		if err := v.Addr().Interface().(units.Quantity).Set(s); err != nil {
			d.Errorf(n.Pos, path, "%v", err)
		}
		return
	}

	switch t.Kind() {
	case reflect.Pointer:
		nv := reflect.New(t.Elem())
		d.Decode(n, path, nv.Elem())
		v.Set(nv)
	case reflect.String:
		if s, ok := d.scalarOf(n, path, t); ok {
			v.SetString(s)
		}
	case reflect.Bool:
		s, ok := d.scalarOf(n, path, t)
		if !ok {
			return
		}
		if n.Quoted && !strings.Contains(n.Value, "${") {
			d.Errorf(n.Pos, path, "expected true or false without quotes, got %q", s)
			return
		}
		switch s {
		case "true":
			v.SetBool(true)
		case "false":
			v.SetBool(false)
		default:
			d.Errorf(n.Pos, path, "expected true or false, got %q", s)
		}
	case reflect.Int, reflect.Int64, reflect.Int32:
		s, ok := d.scalarOf(n, path, t)
		if !ok {
			return
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil || (n.Quoted && !strings.Contains(n.Value, "${")) {
			d.Errorf(n.Pos, path, "expected an integer, got %q", s)
			return
		}
		v.SetInt(i)
	case reflect.Slice:
		if n.Kind != List {
			d.Errorf(n.Pos, path, "expected a list, got a %s (write [a, b] or one '- item' per line)", n.Kind)
			return
		}
		out := reflect.MakeSlice(t, len(n.Items), len(n.Items))
		for i, it := range n.Items {
			d.Decode(it, fmt.Sprintf("%s[%d]", path, i), out.Index(i))
		}
		v.Set(out)
	case reflect.Map:
		if n.Kind != Map {
			d.Errorf(n.Pos, path, "expected a mapping, got a %s", n.Kind)
			return
		}
		out := reflect.MakeMapWithSize(t, len(n.Pairs))
		for _, p := range n.Pairs {
			ev := reflect.New(t.Elem()).Elem()
			d.Decode(p.Value, join(path, p.Key), ev)
			out.SetMapIndex(reflect.ValueOf(p.Key), ev)
		}
		v.Set(out)
	case reflect.Struct:
		d.DecodeStruct(n, path, v)
	default:
		panic(fmt.Sprintf("spec: unsupported field type %s", t))
	}
}

func (d *Decoder) scalarOf(n *Node, path string, t reflect.Type) (string, bool) {
	if n.Kind != Scalar {
		d.Errorf(n.Pos, path, "expected %s, got a %s", TypeName(t), n.Kind)
		return "", false
	}
	return d.Scalar(n, path)
}

// DecodeStruct decodes a mapping into the union of fields of one or more
// struct targets. Unknown keys and missing required keys are errors.
func (d *Decoder) DecodeStruct(n *Node, path string, targets ...reflect.Value) {
	if n.Kind != Map {
		d.Errorf(n.Pos, path, "expected a mapping of keys, got a %s", n.Kind)
		return
	}
	type slot struct {
		f Field
		v reflect.Value
	}
	slots := map[string]slot{}
	var known []string
	for _, t := range targets {
		for _, f := range Fields(t.Type()) {
			slots[f.Key] = slot{f, t.FieldByIndex(f.Index)}
			known = append(known, f.Key)
		}
	}
	seen := map[string]bool{}
	for _, p := range n.Pairs {
		s, ok := slots[p.Key]
		if !ok {
			msg := fmt.Sprintf("unknown key %q", p.Key)
			if sug := Suggest(p.Key, known); sug != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", sug)
			} else {
				sorted := append([]string(nil), known...)
				sort.Strings(sorted)
				msg += " (allowed: " + strings.Join(sorted, ", ") + ")"
			}
			d.Errorf(p.KeyPos, path, "%s", msg)
			continue
		}
		seen[p.Key] = true
		fp := join(path, p.Key)
		before := len(d.Errs)
		if len(s.f.Enum) > 0 && p.Value.Kind == Scalar {
			if val, ok := d.Scalar(p.Value, fp); ok && !contains(s.f.Enum, val) {
				d.Errorf(p.Value.Pos, fp, "must be one of %s, got %q", strings.Join(s.f.Enum, ", "), val)
				continue
			}
		}
		d.Decode(p.Value, fp, s.v)
		if len(d.Errs) == before && s.f.Threshold != "" {
			if dc, ok := s.v.Addr().Interface().(DirectionChecker); ok {
				if err := dc.CheckDirection(s.f.Threshold); err != nil {
					d.Errorf(p.Value.Pos, fp, "%v", err)
				}
			}
		}
	}
	for _, key := range known {
		if s := slots[key]; s.f.Required && !seen[key] {
			d.Errorf(n.Pos, path, "missing required key %q", key)
		}
	}
	for _, t := range targets {
		if val, ok := t.Addr().Interface().(Validator); ok {
			if err := val.Validate(); err != nil {
				d.Errorf(n.Pos, path, "%v", err)
			}
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Field is the metadata of one config key, derived from struct tags:
//
//	yaml:"key"        config key (required for exported config fields)
//	doc:"..."         description for schema and docs
//	required:"true"   key must be present
//	enum:"a,b"        allowed string values
//	threshold:"above" warn/fail thresholds where higher values are worse ("below": lower is worse)
type Field struct {
	Key       string
	Index     []int
	Type      reflect.Type
	Doc       string
	Required  bool
	Enum      []string
	Threshold string
}

// Fields lists the config fields of a struct type.
func Fields(t reflect.Type) []Field {
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		key := sf.Tag.Get("yaml")
		if key == "" || key == "-" || !sf.IsExported() {
			continue
		}
		f := Field{
			Key:       key,
			Index:     sf.Index,
			Type:      sf.Type,
			Doc:       sf.Tag.Get("doc"),
			Required:  sf.Tag.Get("required") == "true",
			Threshold: sf.Tag.Get("threshold"),
		}
		if e := sf.Tag.Get("enum"); e != "" {
			f.Enum = strings.Split(e, ",")
		}
		out = append(out, f)
	}
	return out
}

// Suggest returns the closest candidate to s, or "" if none is close.
// A key like "expect_status" suggests the nested form "expect.status".
func Suggest(s string, candidates []string) string {
	norm := func(x string) string { return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(x)) }
	best, bestD := "", 1<<30
	for _, c := range candidates {
		if norm(c) == norm(s) {
			return c
		}
		if strings.HasPrefix(s, c+"_") || strings.HasPrefix(s, c+"-") {
			return c + "." + s[len(c)+1:]
		}
		if dd := levenshtein(norm(s), norm(c)); dd < bestD {
			best, bestD = c, dd
		}
	}
	limit := 2
	if len(s) <= 4 {
		limit = 1
	}
	if bestD <= limit {
		return best
	}
	return ""
}

// levenshtein is the optimal-string-alignment distance (counts transpositions as 1).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	var prevprev []int
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prevprev[j-2]+1) // transposition
			}
		}
		prevprev, prev = prev, cur
	}
	return prev[len(rb)]
}

// TypeName is a human-readable type name for errors and docs.
func TypeName(t reflect.Type) string {
	if reflect.PointerTo(t).Implements(quantityType) {
		return reflect.New(t).Interface().(units.Quantity).Kind()
	}
	if n, ok := reflect.New(t).Interface().(interface{ TypeName() string }); ok {
		return n.TypeName()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return TypeName(t.Elem())
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int64, reflect.Int32:
		return "integer"
	case reflect.Slice:
		return "list of " + TypeName(t.Elem())
	case reflect.Map:
		return "map of " + TypeName(t.Elem())
	case reflect.Struct:
		return "mapping"
	}
	return t.String()
}
