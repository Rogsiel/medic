package config

import (
	"fmt"
	"regexp"
	"strings"
)

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Interpolate expands ${VAR} and ${VAR:-default} in s. "$${" produces a
// literal "${". An unset variable without a default is an error.
func Interpolate(s string, lookup func(string) (string, bool)) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(s[i:], "${") {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated ${ in %q", s)
		}
		expr := s[i+2 : i+end]
		name, def, hasDef := strings.Cut(expr, ":-")
		if !varName.MatchString(name) {
			return "", fmt.Errorf("invalid variable reference ${%s}: use ${NAME} or ${NAME:-default}", expr)
		}
		val, ok := lookup(name)
		switch {
		case ok && val != "":
			b.WriteString(val)
		case hasDef:
			b.WriteString(def)
		case ok:
			// set but empty: honour it
		default:
			return "", fmt.Errorf("environment variable %s is not set (use ${%s:-default} for a fallback)", name, name)
		}
		i += end + 1
	}
	return b.String(), nil
}
