// Package spec turns YAML into typed Go structs strictly, and derives JSON
// Schema and reference docs from the same struct definitions.
//
// The YAML is first converted into a small position-annotated Node tree. The
// decoder then walks Go types by reflection, so struct tags are the single
// source of truth for keys, docs, required-ness and enums.
package spec

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
)

// Kind is the shape of a Node.
type Kind int

const (
	Null Kind = iota
	Scalar
	Map
	List
)

func (k Kind) String() string {
	return [...]string{"empty value", "scalar", "mapping", "list"}[k]
}

// Pos is a 1-based line/column position in the source.
type Pos struct{ Line, Col int }

// Pair is one key/value entry of a mapping, in source order.
type Pair struct {
	Key    string
	KeyPos Pos
	Value  *Node
}

// Node is a minimal YAML node.
type Node struct {
	Kind   Kind
	Pos    Pos
	Value  string // scalar text as written (without quotes)
	Quoted bool   // scalar was quoted or a block literal
	Pairs  []Pair
	Items  []*Node
}

// Get returns the value for key in a mapping node, or nil.
func (n *Node) Get(key string) *Node {
	if n == nil || n.Kind != Map {
		return nil
	}
	for _, p := range n.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return nil
}

// Set replaces or appends key in a mapping node.
func (n *Node) Set(key string, v *Node) {
	for i, p := range n.Pairs {
		if p.Key == key {
			n.Pairs[i].Value = v
			return
		}
	}
	n.Pairs = append(n.Pairs, Pair{Key: key, KeyPos: v.Pos, Value: v})
}

// Error is a config error anchored to a source position.
type Error struct {
	Pos  Pos    `json:"-"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"column,omitempty"`
	Path string `json:"path,omitempty"`
	Msg  string `json:"message"`
}

func (e Error) Error() string {
	var b strings.Builder
	if e.Pos.Line > 0 {
		fmt.Fprintf(&b, "line %d:%d: ", e.Pos.Line, e.Pos.Col)
	}
	if e.Path != "" {
		b.WriteString(e.Path + ": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// Errors is a list of config errors, sorted by position.
type Errors []Error

func (es Errors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

// Sort orders errors by source position.
func (es Errors) Sort() {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].Pos.Line != es[j].Pos.Line {
			return es[i].Pos.Line < es[j].Pos.Line
		}
		return es[i].Pos.Col < es[j].Pos.Col
	})
}

// AsErrors unwraps err into Errors if possible.
func AsErrors(err error) (Errors, bool) {
	var es Errors
	if errors.As(err, &es) {
		return es, true
	}
	var e Error
	if errors.As(err, &e) {
		return Errors{e}, true
	}
	return nil, false
}

// Parse parses a single YAML document into a Node tree.
// Anchors, aliases, merge keys and tags are rejected: configs must be explicit.
func Parse(src []byte) (*Node, error) {
	f, err := parser.ParseBytes(src, 0)
	if err != nil {
		return nil, syntaxError(err)
	}
	var docs []*ast.DocumentNode
	for _, d := range f.Docs {
		if d != nil && d.Body != nil {
			docs = append(docs, d)
		}
	}
	if len(docs) == 0 {
		return &Node{Kind: Null, Pos: Pos{1, 1}}, nil
	}
	if len(docs) > 1 {
		return nil, Error{Pos: pos(docs[1].Body), Msg: "multiple YAML documents are not supported"}
	}
	var errs Errors
	n := convert(docs[0].Body, &errs)
	if len(errs) > 0 {
		errs.Sort()
		return nil, errs
	}
	return n, nil
}

// ParseValue parses a single YAML value (used for CLI --set values).
func ParseValue(s string) (*Node, error) {
	n, err := Parse([]byte("v: " + s))
	if err != nil {
		return nil, err
	}
	v := n.Get("v")
	if v == nil {
		return &Node{Kind: Null}, nil
	}
	return v, nil
}

func syntaxError(err error) error {
	msg := err.Error()
	// goccy errors look like "[3:5] message\n  source excerpt"; keep the first line.
	first := strings.SplitN(msg, "\n", 2)[0]
	var line, col int
	if _, scanErr := fmt.Sscanf(first, "[%d:%d]", &line, &col); scanErr == nil {
		if i := strings.Index(first, "]"); i >= 0 {
			first = strings.TrimSpace(first[i+1:])
		}
	}
	return Error{Pos: Pos{line, col}, Msg: "invalid YAML: " + first}
}

func pos(n ast.Node) Pos {
	if n == nil {
		return Pos{}
	}
	t := n.GetToken()
	if t == nil || t.Position == nil {
		return Pos{}
	}
	return Pos{t.Position.Line, t.Position.Column}
}

func convert(n ast.Node, errs *Errors) *Node {
	if n == nil {
		return &Node{Kind: Null}
	}
	p := pos(n)
	switch v := n.(type) {
	case *ast.NullNode:
		return &Node{Kind: Null, Pos: p}
	case *ast.StringNode:
		tt := v.GetToken().Type
		return &Node{Kind: Scalar, Pos: p, Value: v.Value, Quoted: tt == token.SingleQuoteType || tt == token.DoubleQuoteType}
	case *ast.LiteralNode:
		return &Node{Kind: Scalar, Pos: p, Value: v.Value.Value, Quoted: true}
	case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode, *ast.InfinityNode, *ast.NanNode:
		return &Node{Kind: Scalar, Pos: p, Value: n.GetToken().Value}
	case *ast.MappingNode:
		out := &Node{Kind: Map, Pos: p}
		for _, mv := range v.Values {
			addPair(out, mv, errs)
		}
		return out
	case *ast.MappingValueNode:
		out := &Node{Kind: Map, Pos: pos(v.Key)}
		addPair(out, v, errs)
		return out
	case *ast.SequenceNode:
		out := &Node{Kind: List, Pos: p}
		for _, it := range v.Values {
			out.Items = append(out.Items, convert(it, errs))
		}
		return out
	case *ast.AnchorNode, *ast.AliasNode:
		*errs = append(*errs, Error{Pos: p, Msg: "YAML anchors and aliases are not supported; use 'defaults:' for shared settings"})
	case *ast.MergeKeyNode:
		*errs = append(*errs, Error{Pos: p, Msg: "YAML merge keys (<<) are not supported; use 'defaults:' for shared settings"})
	case *ast.TagNode:
		*errs = append(*errs, Error{Pos: p, Msg: "YAML tags (!!type) are not supported; write the value directly"})
	default:
		*errs = append(*errs, Error{Pos: p, Msg: fmt.Sprintf("unsupported YAML construct (%s)", n.Type())})
	}
	return &Node{Kind: Null, Pos: p}
}

func addPair(out *Node, mv *ast.MappingValueNode, errs *Errors) {
	if _, ok := mv.Key.(*ast.MergeKeyNode); ok {
		*errs = append(*errs, Error{Pos: pos(mv.Key), Msg: "YAML merge keys (<<) are not supported; use 'defaults:' for shared settings"})
		return
	}
	key := mv.Key.GetToken().Value
	kp := pos(mv.Key)
	val := convert(mv.Value, errs)
	if val.Pos.Line == 0 {
		val.Pos = kp
	}
	out.Pairs = append(out.Pairs, Pair{Key: key, KeyPos: kp, Value: val})
}
