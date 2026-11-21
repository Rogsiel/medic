package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/Rogsiel/medic/internal/check"
	"github.com/Rogsiel/medic/internal/units"
)

func init() {
	check.Register(&check.Type{
		Name:       "tls",
		Summary:    "Verify a TLS certificate chain and how many days it has left.",
		New:        func() check.Checker { return &TLS{Expect: TLSExpect{DaysLeft: check.TH[units.Days]("30d", "7d")}} },
		Positional: "target",
		Example: `type: tls
target: example.com:443
expect:
  days_left: { warn: 30d, fail: 7d }`,
	})
}

// TLS checks a server certificate.
type TLS struct {
	Addr       string    `yaml:"target" required:"true" doc:"host or host:port (port defaults to 443)."`
	ServerName string    `yaml:"server_name" doc:"Name to verify the certificate against (default: the host)."`
	Expect     TLSExpect `yaml:"expect" doc:"What counts as healthy."`
}

// TLSExpect are TLS expectations.
type TLSExpect struct {
	DaysLeft check.DaysTH `yaml:"days_left" threshold:"below" doc:"Days until the leaf certificate expires."`
}

func (c *TLS) Target() string { return c.Addr }

func (c *TLS) Validate() error {
	if c.Addr == "" {
		return nil
	}
	_, _, err := splitHostPort(c.Addr, 443)
	return err
}

func (c *TLS) Run(ctx context.Context) check.Result {
	host, port, _ := splitHostPort(c.Addr, 443)
	sni := c.ServerName
	if sni == "" {
		sni = host
	}
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS10}}
	conn, err := d.DialContext(ctx, "tcp", joinHostPort(host, port))
	if err != nil {
		var cv *tls.CertificateVerificationError
		var ue x509.UnknownAuthorityError
		var he x509.HostnameError
		var ie x509.CertificateInvalidError
		switch {
		case errors.As(err, &he):
			return failed("certificate is not valid for "+sni+": "+he.Error(), nil, "Serve a certificate whose SANs include this name.")
		case errors.As(err, &ue):
			return failed("certificate signed by an unknown authority", nil, "Serve the full chain (leaf + intermediates) and use a trusted CA.")
		case errors.As(err, &ie):
			return failed("invalid certificate: "+ie.Error(), nil, "Renew the certificate.")
		case errors.As(err, &cv):
			return failed("certificate verification failed: "+cv.Err.Error(), nil, "")
		}
		return failed("TLS handshake failed: "+netErr(err), nil, "Check that the port speaks TLS and is reachable.")
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	leaf := state.PeerCertificates[0]
	left := time.Until(leaf.NotAfter)
	days := int(math.Floor(left.Hours() / 24))
	details := map[string]any{
		"subject":   leaf.Subject.CommonName,
		"issuer":    leaf.Issuer.CommonName,
		"sans":      leaf.DNSNames,
		"not_after": leaf.NotAfter.UTC().Format(time.RFC3339),
		"days_left": days,
		"protocol":  tls.VersionName(state.Version),
	}
	st := c.Expect.DaysLeft.Below(float64(days))
	msg := fmt.Sprintf("expires in %d days (%s)", days, leaf.NotAfter.UTC().Format("2006-01-02"))
	r := result(st, msg, details)
	if st != check.Passed {
		r.Remediation = "Renew the certificate (check that auto-renewal is running)."
	}
	return r
}
