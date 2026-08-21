package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// inlineable maps a directive to the position of its file argument. These are
// the options whose referenced file can be folded into an inline block, which
// is what lets a user drop a config plus a handful of certificate files and end
// up with one portable profile.
var inlineable = map[string]int{
	"ca":           0,
	"cert":         0,
	"key":          0,
	"dh":           0,
	"extra-certs":  0,
	"tls-auth":     0,
	"tls-crypt":    0,
	"tls-crypt-v2": 0,
	"secret":       0,
	"crl-verify":   0,
	"pkcs11-id":    -1, // token reference, not a file
}

// keyDirectionCarriers are the two options that take an optional direction
// argument after the filename. Inline blocks cannot carry that argument, so it
// has to be re-emitted as a separate "key-direction" directive or the tunnel
// fails to authenticate with a confusing error.
var keyDirectionCarriers = map[string]bool{
	"tls-auth": true,
	"secret":   true,
}

// binaryReferences are file options we must not inline, because the file is
// binary and openvpn only accepts them as real paths. They are copied next to
// the profile instead.
var binaryReferences = map[string]int{
	"pkcs12": 0,
}

// Asset is a file that had to be copied alongside the profile rather than
// folded into it.
type Asset struct {
	// Name is the filename as it will sit next to the config.
	Name string
	// Data is the file contents.
	Data []byte
}

// Warning is something the user should know about a profile we imported. These
// are surfaced in the UI rather than swallowed: a profile that silently half
// works is worse than one that says what is missing.
type Warning struct {
	// Directive is the option the warning relates to, if any.
	Directive string
	// Message is written for a person, not a log file.
	Message string
}

func (w Warning) String() string {
	if w.Directive == "" {
		return w.Message
	}
	return w.Directive + ": " + w.Message
}

// Normalised is the result of turning a dropped config into something we can
// store and hand to openvpn.
type Normalised struct {
	// Config is the rewritten config, self-contained apart from Assets.
	Config *Config
	// Assets are files that could not be inlined and must be written beside it.
	Assets []Asset
	// Warnings describe anything the user should look at.
	Warnings []Warning
	// Summary is what the UI shows about this profile.
	Summary Summary
}

// Summary is the descriptive metadata the UI needs without re-parsing.
type Summary struct {
	// Remotes are the servers this profile can connect to, "host:port".
	Remotes []string
	// Proto is "udp", "tcp" or "" when unspecified.
	Proto string
	// Device is "tun" or "tap".
	Device string
	// NeedsCredentials reports whether openvpn will ask us for a username and
	// password (--auth-user-pass with no file argument).
	NeedsCredentials bool
	// NeedsKeyPassphrase reports whether the private key looks encrypted, so we
	// should expect a passphrase prompt.
	NeedsKeyPassphrase bool
	// UsesRedirectGateway reports whether all traffic goes through the tunnel.
	UsesRedirectGateway bool
}

// Normalise rewrites cfg so every referenced file is either inlined or returned
// as an asset. baseDir is where relative paths in the config resolve from.
func Normalise(cfg *Config, baseDir string) (*Normalised, error) {
	out := &Normalised{Config: &Config{}}

	// Track whether a direction argument needs re-emitting, and whether the
	// config already says so itself.
	keyDirection := ""
	hasKeyDirection := cfg.Has("key-direction")

	for _, d := range cfg.Directives {
		if d.Inline {
			out.Config.Directives = append(out.Config.Directives, d)
			continue
		}

		if pos, ok := binaryReferences[d.Name]; ok && pos >= 0 && len(d.Args) > pos {
			asset, err := readReference(baseDir, d.Arg(pos))
			if err != nil {
				out.warn(d.Name, "%v. The profile will not connect until this file is supplied.", err)
				out.Config.Directives = append(out.Config.Directives, d)
				continue
			}
			out.Assets = append(out.Assets, asset)
			rewritten := d
			rewritten.Args = append([]string{asset.Name}, d.Args[pos+1:]...)
			out.Config.Directives = append(out.Config.Directives, rewritten)
			continue
		}

		pos, ok := inlineable[d.Name]
		if !ok || pos < 0 || len(d.Args) <= pos {
			out.Config.Directives = append(out.Config.Directives, d)
			continue
		}

		// Not every argument in these positions is a filename. "[inline]" marks
		// a body that is already inline, and "dh none" is openvpn's way of
		// saying it needs no DH parameters at all -- treating that as a path
		// produces a warning about a missing file called "none".
		if isNotAPath(d.Arg(pos)) {
			out.Config.Directives = append(out.Config.Directives, d)
			continue
		}

		asset, err := readReference(baseDir, d.Arg(pos))
		if err != nil {
			out.warn(d.Name, "%v. Drop the file in alongside the profile and import again.", err)
			out.Config.Directives = append(out.Config.Directives, d)
			continue
		}

		if keyDirectionCarriers[d.Name] && len(d.Args) > pos+1 {
			keyDirection = d.Arg(pos + 1)
		}

		out.Config.Directives = append(out.Config.Directives, Directive{
			Name:   d.Name,
			Body:   string(asset.Data),
			Inline: true,
			Line:   d.Line,
		})
	}

	// An inline tls-auth/secret block loses the direction argument, so restate
	// it explicitly.
	if keyDirection != "" && !hasKeyDirection {
		out.Config.Directives = append(out.Config.Directives, Directive{
			Name: "key-direction",
			Args: []string{keyDirection},
		})
	}

	out.Summary = summarise(out.Config)
	out.Warnings = append(out.Warnings, reviewWarnings(out.Config, out.Summary)...)
	return out, nil
}

// isNotAPath reports whether an argument in a file position is one of
// openvpn's sentinel values rather than a filename.
func isNotAPath(arg string) bool {
	switch strings.ToLower(arg) {
	case "", "[inline]", "none":
		return true
	default:
		return false
	}
}

// readReference loads a file referenced by the config.
func readReference(baseDir, ref string) (Asset, error) {
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, ref)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Asset{}, fmt.Errorf("referenced file %q was not found next to the profile", ref)
		}
		return Asset{}, fmt.Errorf("could not read %q: %w", ref, err)
	}
	return Asset{Name: filepath.Base(ref), Data: data}, nil
}

func (n *Normalised) warn(directive, format string, args ...any) {
	n.Warnings = append(n.Warnings, Warning{
		Directive: directive,
		Message:   fmt.Sprintf(format, args...),
	})
}

// summarise extracts the descriptive fields the UI shows.
func summarise(cfg *Config) Summary {
	s := Summary{Proto: "", Device: "tun"}

	for _, d := range cfg.All("remote") {
		host := d.Arg(0)
		if host == "" {
			continue
		}
		if port := d.Arg(1); port != "" {
			s.Remotes = append(s.Remotes, host+":"+port)
		} else {
			s.Remotes = append(s.Remotes, host)
		}
	}

	if d := cfg.First("proto"); d != nil {
		s.Proto = strings.ToLower(d.Arg(0))
	}
	if d := cfg.First("dev"); d != nil {
		if v := strings.ToLower(d.Arg(0)); strings.HasPrefix(v, "tap") {
			s.Device = "tap"
		}
	}
	if d := cfg.First("auth-user-pass"); d != nil {
		// With a file argument openvpn reads credentials itself; without one it
		// asks us over the management interface.
		s.NeedsCredentials = len(d.Args) == 0
	}
	if d := cfg.First("key"); d != nil && d.Inline {
		s.NeedsKeyPassphrase = strings.Contains(d.Body, "ENCRYPTED")
	}
	s.UsesRedirectGateway = cfg.Has("redirect-gateway")
	return s
}

// reviewWarnings flags things that will bite the user at connect time, so the
// import screen can say so while they are still looking at it.
func reviewWarnings(cfg *Config, s Summary) []Warning {
	var out []Warning

	if len(s.Remotes) == 0 {
		out = append(out, Warning{
			Directive: "remote",
			Message:   "No server address in this profile, so there is nothing to connect to.",
		})
	}
	if s.Device == "tap" {
		out = append(out, Warning{
			Directive: "dev",
			Message:   "This profile needs a TAP adapter (layer 2). Make sure the TAP driver was installed with OpenVPN.",
		})
	}
	if d := cfg.First("auth-user-pass"); d != nil && len(d.Args) > 0 {
		out = append(out, Warning{
			Directive: "auth-user-pass",
			Message:   "Credentials are read from a file on disk. Remove the filename to have them stored in the Windows credential manager instead.",
		})
	}
	if cfg.Has("askpass") {
		out = append(out, Warning{
			Directive: "askpass",
			Message:   "Private key passphrases are prompted for in the app, so this option is ignored.",
		})
	}
	for _, name := range []string{"up", "down", "route-up", "ipchange", "client-connect"} {
		if cfg.Has(name) {
			out = append(out, Warning{
				Directive: name,
				Message:   "This profile runs a script. It will only work if the script exists on this machine.",
			})
		}
	}
	if !cfg.Has("ca") && !cfg.Has("pkcs12") && !cfg.Has("peer-fingerprint") && !cfg.Has("secret") {
		out = append(out, Warning{
			Directive: "ca",
			Message:   "No certificate authority in this profile. openvpn will refuse to start without one.",
		})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Directive < out[j].Directive })
	return out
}
