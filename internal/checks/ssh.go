package checks

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Rogsiel/medic/internal/check"
)

func init() {
	check.Register(&check.Type{
		Name:       "ssh",
		Summary:    "Check that an SSH server answers, and optionally that key authentication works.",
		New:        func() check.Checker { return &SSH{} },
		Positional: "target",
		Example: `type: ssh
target: bastion.example.com:22
auth: true
user: deploy
key: ~/.ssh/id_ed25519`,
	})
}

// SSH checks an SSH daemon.
type SSH struct {
	Addr       string    `yaml:"target" required:"true" doc:"host or host:port (port defaults to 22)."`
	Auth       bool      `yaml:"auth" doc:"Log in with a key to prove authentication works (requires user and key)."`
	User       string    `yaml:"user" doc:"User for auth."`
	Key        string    `yaml:"key" doc:"Private key file for auth (unencrypted; ~ is expanded)."`
	KnownHosts string    `yaml:"known_hosts" doc:"known_hosts file used to verify the host key during auth (default ~/.ssh/known_hosts)."`
	Insecure   bool      `yaml:"insecure" doc:"Skip host key verification during auth (shown in the report). Avoid."`
	Expect     SSHExpect `yaml:"expect" doc:"What counts as healthy."`
}

// SSHExpect are SSH expectations.
type SSHExpect struct {
	BannerContains string `yaml:"banner_contains" doc:"Text the server version banner must contain (e.g. OpenSSH)."`
}

func (c *SSH) Target() string { return c.Addr }

func (c *SSH) Validate() error {
	if c.Addr != "" {
		if _, _, err := splitHostPort(c.Addr, 22); err != nil {
			return err
		}
	}
	if c.Auth && (c.User == "" || c.Key == "") {
		return fmt.Errorf("auth: true requires 'user' and 'key'")
	}
	if !c.Auth && (c.User != "" || c.Key != "" || c.KnownHosts != "" || c.Insecure) {
		return fmt.Errorf("user, key, known_hosts and insecure only apply with auth: true")
	}
	return nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func (c *SSH) Run(ctx context.Context) check.Result {
	host, port, _ := splitHostPort(c.Addr, 22)
	addr := joinHostPort(host, port)
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return failed("connect failed: "+netErr(err), nil, "Check that sshd is running and the port is reachable.")
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	details := map[string]any{}

	if !c.Auth {
		line, err := bufio.NewReader(conn).ReadString('\n')
		banner := strings.TrimSpace(line)
		if !strings.HasPrefix(banner, "SSH-") {
			if err != nil {
				return failed("no SSH banner: "+netErr(err), details, "The port is open but is not answering as an SSH server.")
			}
			return failed(fmt.Sprintf("not an SSH server (got %q)", firstLine(banner, 60)), details, "")
		}
		details["banner"] = banner
		if c.Expect.BannerContains != "" && !strings.Contains(banner, c.Expect.BannerContains) {
			return failed(fmt.Sprintf("banner %q does not contain %q", banner, c.Expect.BannerContains), details, "")
		}
		return result(check.Passed, fmt.Sprintf("%s in %s", banner, dur(time.Since(start))), details)
	}

	keyBytes, err := os.ReadFile(expandHome(c.Key))
	if err != nil {
		return failed("cannot read key: "+err.Error(), details, "")
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return failed("cannot parse key: "+err.Error(), details, "Use an unencrypted key file for health checks.")
	}
	var hostKeyCB ssh.HostKeyCallback
	if c.Insecure {
		hostKeyCB = ssh.InsecureIgnoreHostKey() //nolint:gosec // opt-in, reported
		details["insecure"] = true
	} else {
		kh := c.KnownHosts
		if kh == "" {
			kh = "~/.ssh/known_hosts"
		}
		cb, err := knownhosts.New(expandHome(kh))
		if err != nil {
			return failed("cannot load known_hosts: "+err.Error(), details, "Add the host with ssh-keyscan, or set known_hosts.")
		}
		hostKeyCB = cb
	}
	cfg := &ssh.ClientConfig{User: c.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: hostKeyCB}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		msg := err.Error()
		rem := "Check that the public key is in the user's authorized_keys."
		if strings.Contains(msg, "knownhosts") || strings.Contains(msg, "host key") {
			rem = "The host key is unknown or changed; verify it and update known_hosts."
		}
		return failed("auth failed: "+msg, details, rem)
	}
	client := ssh.NewClient(sc, chans, reqs)
	defer client.Close()
	banner := string(client.ServerVersion())
	details["banner"] = banner
	if c.Expect.BannerContains != "" && !strings.Contains(banner, c.Expect.BannerContains) {
		return failed(fmt.Sprintf("banner %q does not contain %q", banner, c.Expect.BannerContains), details, "")
	}
	return result(check.Passed, fmt.Sprintf("authenticated as %s (%s) in %s", c.User, banner, dur(time.Since(start))), details)
}
