package runner

import (
	"sort"
	"strings"
)

// Masker replaces secret values with *** everywhere in results.
type Masker struct {
	r *strings.Replacer
}

// MinSecretLen is the shortest value that is masked; shorter values would
// shred ordinary text and are not plausible secrets.
const MinSecretLen = 4

// NewMasker builds a masker for the given secret values (values shorter than
// MinSecretLen are ignored).
func NewMasker(secrets []string) *Masker {
	vals := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if len(s) >= MinSecretLen {
			vals = append(vals, s)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	// Longest first so overlapping secrets are fully masked.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, "***")
	}
	return &Masker{r: strings.NewReplacer(pairs...)}
}

// String masks s. A nil Masker is a no-op.
func (m *Masker) String(s string) string {
	if m == nil {
		return s
	}
	return m.r.Replace(s)
}

// Result masks every string in r in place.
func (m *Masker) Result(r *Result) {
	if m == nil {
		return
	}
	r.Name = m.String(r.Name)
	r.Message = m.String(r.Message)
	r.Target = m.String(r.Target)
	r.Remediation = m.String(r.Remediation)
	for k, v := range r.Details {
		r.Details[k] = m.value(v)
	}
}

func (m *Masker) value(v any) any {
	switch x := v.(type) {
	case string:
		return m.String(x)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = m.String(s)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = m.value(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = m.value(e)
		}
		return out
	}
	return v
}
