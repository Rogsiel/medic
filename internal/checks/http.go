package checks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "http",
		Summary:    "Request a URL and check status, body, JSON fields and latency.",
		New:        func() check.Checker { return &HTTP{Method: "GET", FollowRedirects: true} },
		Positional: "url",
		Example: `type: http
url: https://api.example.com/health
expect:
  status: [200]
  json: { "$.status": up }
  latency: { warn: 300ms, fail: 2s }`,
	})
}

const maxBody = 1 << 20

// HTTP checks an HTTP(S) endpoint.
type HTTP struct {
	URL             string            `yaml:"url" required:"true" doc:"URL to request (http:// or https://)."`
	Method          string            `yaml:"method" enum:"GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS" doc:"HTTP method."`
	Headers         map[string]string `yaml:"headers" doc:"Request headers. Use ${VAR} plus 'secrets:' for tokens."`
	Body            string            `yaml:"body" doc:"Request body."`
	FollowRedirects bool              `yaml:"follow_redirects" doc:"Follow up to 10 redirects."`
	Insecure        bool              `yaml:"insecure" doc:"Skip TLS certificate verification (shown in the report). Avoid."`
	Expect          HTTPExpect        `yaml:"expect" doc:"What counts as healthy."`
}

// HTTPExpect are HTTP expectations.
type HTTPExpect struct {
	Status       []int             `yaml:"status" doc:"Accepted status codes (default: any 2xx)."`
	BodyContains string            `yaml:"body_contains" doc:"Text the response body must contain."`
	JSON         map[string]string `yaml:"json" doc:"Fields the JSON body must have: path -> expected value. Paths look like $.status or $.items[0].name."`
	Latency      check.DurationTH  `yaml:"latency" threshold:"above" doc:"Total response time limits."`
}

func (c *HTTP) Target() string { return c.URL }

func (c *HTTP) Validate() error {
	if c.URL != "" {
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url %q must be an absolute http:// or https:// URL", c.URL)
		}
	}
	for _, s := range c.Expect.Status {
		if s < 100 || s > 599 {
			return fmt.Errorf("expect.status: %d is not an HTTP status code", s)
		}
	}
	for p := range c.Expect.JSON {
		if _, err := parseJSONPath(p); err != nil {
			return fmt.Errorf("expect.json: %v", err)
		}
	}
	return nil
}

func (c *HTTP) Run(ctx context.Context) check.Result {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: c.Insecure} //nolint:gosec // opt-in, reported
	client := &http.Client{Transport: transport}
	if !c.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	var body io.Reader
	if c.Body != "" {
		body = strings.NewReader(c.Body)
	}
	req, err := http.NewRequestWithContext(ctx, c.Method, c.URL, body)
	if err != nil {
		return failed("bad request: "+err.Error(), nil, "")
	}
	req.Header.Set("User-Agent", "medic")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return failed("request failed: "+httpErr(err), map[string]any{"elapsed_ms": ms(time.Since(start))},
			"Check that the service is up, the URL is right and DNS/TLS work from this machine.")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	elapsed := time.Since(start)

	details := map[string]any{"status_code": resp.StatusCode, "elapsed_ms": ms(elapsed)}
	if resp.Request != nil && resp.Request.URL.String() != c.URL {
		details["final_url"] = resp.Request.URL.String()
	}
	if c.Insecure {
		details["insecure"] = true
	}
	snippet := func() { details["body"] = firstLine(string(raw), 300) }

	if !statusOK(resp.StatusCode, c.Expect.Status) {
		snippet()
		want := "2xx"
		if len(c.Expect.Status) > 0 {
			want = fmt.Sprint(c.Expect.Status)
		}
		return failed(fmt.Sprintf("status %d (expected %s)", resp.StatusCode, want), details,
			"Inspect the service logs for this endpoint.")
	}
	if c.Expect.BodyContains != "" && !strings.Contains(string(raw), c.Expect.BodyContains) {
		snippet()
		return failed(fmt.Sprintf("body does not contain %q", c.Expect.BodyContains), details, "")
	}
	if len(c.Expect.JSON) > 0 {
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			snippet()
			return failed("body is not valid JSON: "+err.Error(), details, "")
		}
		for _, p := range sortedKeys(c.Expect.JSON) {
			want := c.Expect.JSON[p]
			got, ok := lookupJSON(doc, p)
			if !ok {
				return failed(fmt.Sprintf("JSON field %s is missing", p), details, "")
			}
			if jsonString(got) != want {
				return failed(fmt.Sprintf("JSON field %s is %s (expected %q)", p, compactJSON(got), want), details, "")
			}
		}
	}
	g := newGrade()
	g.add(c.Expect.Latency.Above(elapsed.Seconds()), fmt.Sprintf("slow response: %s (limit %s)", dur(elapsed), c.Expect.Latency))
	msg := fmt.Sprintf("%d in %s", resp.StatusCode, dur(elapsed))
	if c.Insecure {
		msg += " (TLS verification disabled)"
	}
	if g.status != check.Passed {
		msg = g.message(msg)
	}
	return result(g.status, msg, details)
}

func statusOK(code int, want []int) bool {
	if len(want) == 0 {
		return code >= 200 && code < 300
	}
	for _, w := range want {
		if code == w {
			return true
		}
	}
	return false
}

func httpErr(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	s := err.Error()
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(s, "Client.Timeout") {
		return "timed out"
	}
	return netErr(err)
}

// ---- minimal JSONPath: $.a.b[0].c --------------------------------------

type pathStep struct {
	key   string
	index int // -1 when key step
}

var stepRe = regexp.MustCompile(`^(?:\.([A-Za-z_][A-Za-z0-9_-]*)|\[(\d+)\]|\["([^"]*)"\])`)

func parseJSONPath(p string) ([]pathStep, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("path %q must start with $ (e.g. $.status)", p)
	}
	var steps []pathStep
	rest := p[1:]
	for rest != "" {
		m := stepRe.FindStringSubmatch(rest)
		if m == nil {
			return nil, fmt.Errorf("cannot parse path %q near %q (use $.key, $.list[0] or $[\"odd key\"])", p, rest)
		}
		switch {
		case m[1] != "":
			steps = append(steps, pathStep{key: m[1], index: -1})
		case m[2] != "":
			i, _ := strconv.Atoi(m[2])
			steps = append(steps, pathStep{index: i})
		default:
			steps = append(steps, pathStep{key: m[3], index: -1})
		}
		rest = rest[len(m[0]):]
	}
	return steps, nil
}

func lookupJSON(doc any, p string) (any, bool) {
	steps, err := parseJSONPath(p)
	if err != nil {
		return nil, false
	}
	cur := doc
	for _, s := range steps {
		if s.index >= 0 {
			arr, ok := cur.([]any)
			if !ok || s.index >= len(arr) {
				return nil, false
			}
			cur = arr[s.index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[s.key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// jsonString renders a JSON value the way it would be written in YAML.
func jsonString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return compactJSON(v)
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	return firstLine(string(b), 120)
}
