package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseString(t *testing.T, s string) *Config {
	t.Helper()
	cfg, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg
}

func TestParseDirectives(t *testing.T) {
	cfg := parseString(t, `# a comment
; another comment

client
dev tun
proto udp
remote vpn.example.com 1194
remote backup.example.com 443 tcp
verb 3   # trailing comment
`)

	if got := len(cfg.All("remote")); got != 2 {
		t.Errorf("remote count = %d, want 2", got)
	}
	if d := cfg.First("remote"); d.Arg(0) != "vpn.example.com" || d.Arg(1) != "1194" {
		t.Errorf("first remote = %v, want [vpn.example.com 1194]", d.Args)
	}
	if d := cfg.First("verb"); d.Arg(0) != "3" || len(d.Args) != 1 {
		t.Errorf("verb args = %v, want [3] with the trailing comment stripped", d.Args)
	}
	if cfg.Has("a") {
		t.Error("comment text was parsed as a directive")
	}
}

func TestParseQuotedArguments(t *testing.T) {
	cfg := parseString(t, `ca "C:\\certs\\my ca.crt"
cert 'other file.crt'
`)
	if got := cfg.First("ca").Arg(0); got != `C:\certs\my ca.crt` {
		t.Errorf("ca = %q, want the unescaped path with its space intact", got)
	}
	if got := cfg.First("cert").Arg(0); got != "other file.crt" {
		t.Errorf("cert = %q", got)
	}
}

func TestParseInlineBlocks(t *testing.T) {
	cfg := parseString(t, `client
<ca>
-----BEGIN CERTIFICATE-----
AAAA
-----END CERTIFICATE-----
</ca>
dev tun
`)
	ca := cfg.First("ca")
	if ca == nil {
		t.Fatal("inline <ca> block was not parsed")
	}
	if !ca.Inline {
		t.Error("ca.Inline = false, want true")
	}
	if !strings.Contains(ca.Body, "AAAA") {
		t.Errorf("ca body = %q, want the certificate contents", ca.Body)
	}
	// Directives after the block must still be seen.
	if !cfg.Has("dev") {
		t.Error("directive after an inline block was dropped")
	}
}

func TestParseUnterminatedInlineBlockIsAnError(t *testing.T) {
	_, err := Parse(strings.NewReader("<ca>\nAAAA\n"))
	if err == nil {
		t.Fatal("Parse succeeded on an unterminated block, want an error")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Errorf("error = %v, want it to name the unterminated block", err)
	}
}

func TestNormaliseInlinesSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ca.crt", "CA-BODY\n")
	write("client.crt", "CERT-BODY\n")
	write("client.key", "KEY-BODY\n")
	write("ta.key", "TA-BODY\n")

	cfg := parseString(t, `client
dev tun
remote vpn.example.com 1194
ca ca.crt
cert client.crt
key client.key
tls-auth ta.key 1
`)

	n, err := Normalise(cfg, dir)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for name, want := range map[string]string{
		"ca":       "CA-BODY",
		"cert":     "CERT-BODY",
		"key":      "KEY-BODY",
		"tls-auth": "TA-BODY",
	} {
		d := n.Config.First(name)
		if d == nil || !d.Inline {
			t.Errorf("%s was not inlined", name)
			continue
		}
		if !strings.Contains(d.Body, want) {
			t.Errorf("%s body = %q, want it to contain %q", name, d.Body, want)
		}
	}

	// An inline tls-auth block cannot carry the direction argument, so it must
	// be restated or authentication fails at connect time.
	kd := n.Config.First("key-direction")
	if kd == nil {
		t.Fatal("key-direction was not restated after inlining tls-auth")
	}
	if kd.Arg(0) != "1" {
		t.Errorf("key-direction = %q, want 1", kd.Arg(0))
	}

	// The rendered config must round-trip through the parser.
	again := parseString(t, n.Config.String())
	if d := again.First("ca"); d == nil || !d.Inline || !strings.Contains(d.Body, "CA-BODY") {
		t.Error("rendered config does not round-trip the inline ca block")
	}
}

func TestNormaliseWarnsAboutMissingFiles(t *testing.T) {
	cfg := parseString(t, `client
remote vpn.example.com 1194
ca missing.crt
`)
	n, err := Normalise(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if len(n.Warnings) == 0 {
		t.Fatal("no warning for a missing referenced file")
	}
	var found bool
	for _, w := range n.Warnings {
		if w.Directive == "ca" && strings.Contains(w.Message, "not found") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one naming the missing ca file", n.Warnings)
	}
	// The directive must be left alone so the user can fix it and re-import.
	if d := n.Config.First("ca"); d == nil || d.Inline {
		t.Error("missing ca reference should be preserved verbatim, not inlined")
	}
}

func TestNormaliseKeepsAlreadyInlineProfiles(t *testing.T) {
	cfg := parseString(t, `client
remote vpn.example.com 1194
auth-user-pass
<ca>
CA-BODY
</ca>
<key>
-----BEGIN ENCRYPTED PRIVATE KEY-----
</key>
`)
	n, err := Normalise(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if len(n.Summary.Remotes) != 1 || n.Summary.Remotes[0] != "vpn.example.com:1194" {
		t.Errorf("remotes = %v", n.Summary.Remotes)
	}
	if !n.Summary.NeedsCredentials {
		t.Error("NeedsCredentials = false, want true for bare auth-user-pass")
	}
	if !n.Summary.NeedsKeyPassphrase {
		t.Error("NeedsKeyPassphrase = false, want true for an encrypted key")
	}
}

func TestSummaryCredentialsFromFileIsNotAPrompt(t *testing.T) {
	cfg := parseString(t, `client
remote vpn.example.com 1194
auth-user-pass creds.txt
<ca>
CA
</ca>
`)
	n, err := Normalise(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if n.Summary.NeedsCredentials {
		t.Error("NeedsCredentials = true, but openvpn reads them from the file itself")
	}
	var warned bool
	for _, w := range n.Warnings {
		if w.Directive == "auth-user-pass" {
			warned = true
		}
	}
	if !warned {
		t.Error("want a warning that credentials are being read from a file on disk")
	}
}

// TestNormaliseLeavesSentinelsAlone covers arguments that sit in a filename
// position but are not filenames. "dh none" tells openvpn it needs no DH
// parameters, and treating it as a path produced a warning about a missing file
// called "none".
func TestNormaliseLeavesSentinelsAlone(t *testing.T) {
	cfg := parseString(t, `client
remote vpn.example.com 1194
dh none
tls-auth [inline] 1
<ca>
CA
</ca>
`)
	n, err := Normalise(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for _, w := range n.Warnings {
		if w.Directive == "dh" || w.Directive == "tls-auth" {
			t.Errorf("unexpected warning for a sentinel value: %s", w)
		}
	}
	if d := n.Config.First("dh"); d == nil || d.Inline || d.Arg(0) != "none" {
		t.Errorf("dh directive was altered: %+v", d)
	}
}

func TestNormaliseWarnsWhenNoCA(t *testing.T) {
	cfg := parseString(t, "client\nremote vpn.example.com 1194\n")
	n, err := Normalise(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	var found bool
	for _, w := range n.Warnings {
		if w.Directive == "ca" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one about the missing certificate authority", n.Warnings)
	}
}
