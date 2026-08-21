// Package profile reads, normalises and stores OpenVPN client profiles.
//
// The job here is to turn whatever the user dropped on the window -- a lone
// .ovpn, or one with a pile of .crt and .key files beside it -- into a single
// self-contained config file we control, plus enough metadata to describe it in
// the UI without re-reading the config every time.
package profile

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Directive is one option from a config file.
//
// OpenVPN configs have two syntaxes for the same thing: a line like
// "ca ca.crt" naming a file, and an XML-ish block "<ca>...</ca>" carrying the
// contents. Both land here, distinguished by Inline.
type Directive struct {
	// Name is the option without leading dashes, e.g. "remote".
	Name string
	// Args are the whitespace-separated arguments, with quotes removed.
	Args []string
	// Body is the contents of an inline block, empty for plain directives.
	Body string
	// Inline reports whether this came from an <name>...</name> block.
	Inline bool
	// Line is the 1-based line number the directive started on, for diagnostics.
	Line int
}

// Arg returns argument i, or "" if it was not supplied.
func (d Directive) Arg(i int) string {
	if i < len(d.Args) {
		return d.Args[i]
	}
	return ""
}

// Config is a parsed profile. Directive order is preserved, because openvpn
// itself is order-sensitive for a few options and because round-tripping a
// config the user recognises is friendlier than reformatting it.
type Config struct {
	Directives []Directive
}

// First returns the first directive with the given name, or nil.
func (c *Config) First(name string) *Directive {
	for i := range c.Directives {
		if c.Directives[i].Name == name {
			return &c.Directives[i]
		}
	}
	return nil
}

// All returns every directive with the given name.
func (c *Config) All(name string) []Directive {
	var out []Directive
	for _, d := range c.Directives {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// Has reports whether the option is present at all.
func (c *Config) Has(name string) bool { return c.First(name) != nil }

// Parse reads an OpenVPN config.
//
// It is deliberately permissive: unknown options are kept verbatim rather than
// rejected, because the point is to hand the file back to openvpn, which is the
// real authority on what is valid. Only structural problems -- an unterminated
// inline block -- are errors.
func Parse(r io.Reader) (*Config, error) {
	cfg := &Config{}
	sc := bufio.NewScanner(r)
	// Inline blocks can hold a whole certificate chain, so allow long lines and
	// a large total.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		line := strings.TrimSpace(raw)

		if line == "" || isComment(line) {
			continue
		}

		// Inline block: <name> ... </name>
		if name, ok := openTag(line); ok {
			body, endLine, err := readInlineBlock(sc, name, &lineNo)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo, err)
			}
			_ = endLine
			cfg.Directives = append(cfg.Directives, Directive{
				Name:   name,
				Body:   body,
				Inline: true,
				Line:   lineNo,
			})
			continue
		}

		fields := splitArgs(line)
		if len(fields) == 0 {
			continue
		}
		cfg.Directives = append(cfg.Directives, Directive{
			Name: strings.TrimLeft(fields[0], "-"),
			Args: fields[1:],
			Line: lineNo,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return cfg, nil
}

// isComment reports whether a line is a comment. openvpn accepts both "#" and
// ";" as comment markers.
func isComment(line string) bool {
	return strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";")
}

// openTag returns the tag name if the line is an opening inline tag.
func openTag(line string) (string, bool) {
	if !strings.HasPrefix(line, "<") || strings.HasPrefix(line, "</") {
		return "", false
	}
	end := strings.Index(line, ">")
	if end < 0 {
		return "", false
	}
	name := strings.TrimSpace(line[1:end])
	if name == "" || strings.ContainsAny(name, " \t") {
		return "", false
	}
	return name, true
}

// readInlineBlock consumes lines up to the matching closing tag.
func readInlineBlock(sc *bufio.Scanner, name string, lineNo *int) (string, int, error) {
	closing := "</" + name + ">"
	var body strings.Builder
	for sc.Scan() {
		*lineNo++
		line := sc.Text()
		if strings.TrimSpace(line) == closing {
			return body.String(), *lineNo, nil
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	if err := sc.Err(); err != nil {
		return "", *lineNo, err
	}
	return "", *lineNo, fmt.Errorf("unterminated <%s> block", name)
}

// splitArgs splits a directive line into fields, honouring double and single
// quotes and backslash escapes the way openvpn's own parser does.
func splitArgs(line string) []string {
	var (
		fields []string
		cur    strings.Builder
		quote  rune
		esc    bool
		has    bool
	)
	flush := func() {
		if has {
			fields = append(fields, cur.String())
			cur.Reset()
			has = false
		}
	}

	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			has = true
			esc = false
		case r == '\\':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			has = true
		case r == '"' || r == '\'':
			quote = r
			// An empty quoted string is still an argument.
			has = true
		case r == ' ' || r == '\t':
			flush()
		case r == '#' || r == ';':
			// Trailing comment.
			flush()
			return fields
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return fields
}

// String renders the config back out. Inline blocks are emitted last so the
// result reads like a hand-written profile rather than an assembly dump.
func (c *Config) String() string {
	var plain, inline strings.Builder
	for _, d := range c.Directives {
		if d.Inline {
			fmt.Fprintf(&inline, "<%s>\n%s</%s>\n", d.Name, ensureTrailingNewline(d.Body), d.Name)
			continue
		}
		plain.WriteString(d.Name)
		for _, a := range d.Args {
			plain.WriteByte(' ')
			plain.WriteString(quoteArg(a))
		}
		plain.WriteByte('\n')
	}
	if inline.Len() == 0 {
		return plain.String()
	}
	return plain.String() + "\n" + inline.String()
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// quoteArg re-quotes an argument if it contains whitespace.
func quoteArg(a string) string {
	if a == "" {
		return `""`
	}
	if !strings.ContainsAny(a, " \t\"") {
		return a
	}
	return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
}
