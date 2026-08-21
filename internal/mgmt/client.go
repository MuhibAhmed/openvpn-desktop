package mgmt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// passwordPrompt is written by openvpn without a trailing newline, so the read
// loop cannot simply be line-based during the handshake.
const passwordPrompt = "ENTER PASSWORD:"

// eventBuffer is the depth of the event channel. Log lines arrive in bursts
// during connect; anything deeper than this and the consumer is not keeping up.
const eventBuffer = 512

// replyBuffer is the depth of the command-reply channel. Only one command is
// ever in flight, so this only needs slack for a multi-line payload.
const replyBuffer = 256

// commandTimeout bounds how long we wait for a command to be acknowledged.
// openvpn answers from its event loop, so a stall here means it is wedged.
const commandTimeout = 15 * time.Second

// Client is a connection to one openvpn process's management interface.
//
// Commands are strictly serialised: one command is sent, its reply is consumed,
// and only then is the next sent. This is not politeness, it is required.
// openvpn processes a single command per socket read and clears its pending
// output queue on every read, so pipelining commands both drops commands and
// discards real-time notifications that had not been flushed yet.
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	line bytes.Buffer // scratch for readUnit

	// cmdMu serialises whole command/reply exchanges, not just the write.
	cmdMu   sync.Mutex
	writeMu sync.Mutex

	events  chan Event
	replies chan string

	closeOnce sync.Once
	closed    chan struct{}

	errMu sync.Mutex
	err   error

	// Trace, if set, is called for every line in both directions. Set it before
	// the client is used. Direction is "->" for lines we send and "<-" for
	// lines we receive.
	Trace func(direction, line string)
}

func (c *Client) trace(direction, line string) {
	if c.Trace != nil {
		c.Trace(direction, line)
	}
}

// Dial connects to the management interface at addr and authenticates.
//
// password must match what openvpn was given for its --management password
// source. Pass an empty string if the interface was started without one.
func Dial(ctx context.Context, addr, password string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial management interface %s: %w", addr, err)
	}

	c := &Client{
		conn:    conn,
		br:      bufio.NewReader(conn),
		events:  make(chan Event, eventBuffer),
		replies: make(chan string, replyBuffer),
		closed:  make(chan struct{}),
	}

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	if err := c.handshake(password); err != nil {
		conn.Close()
		return nil, err
	}
	// Clear the handshake deadline: from here on the connection is long-lived
	// and may legitimately sit idle.
	conn.SetDeadline(time.Time{})

	go c.readLoop()
	return c, nil
}

// handshake answers the password prompt, if there is one, and waits for
// openvpn to acknowledge that the session is live.
func (c *Client) handshake(password string) error {
	sentPassword := false
	for {
		unit, err := c.readUnit()
		if err != nil {
			return fmt.Errorf("management handshake: %w", err)
		}
		switch {
		case unit == passwordPrompt:
			if sentPassword {
				return errors.New("management interface rejected the password")
			}
			if err := c.write(password); err != nil {
				return err
			}
			sentPassword = true
		case strings.HasPrefix(unit, "SUCCESS:"):
			// "password is correct". The greeting follows but is not required.
			return nil
		case strings.HasPrefix(unit, ">INFO:"):
			// Greeting; arrives with or without a password.
			return nil
		case strings.HasPrefix(unit, "ERROR:"):
			return fmt.Errorf("management interface: %s", unit)
		}
	}
}

// Events returns the channel of decoded notifications. It is closed when the
// connection ends; check Err for the reason.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when the read loop exits, i.e. when openvpn went away.
func (c *Client) Done() <-chan struct{} { return c.closed }

// Err reports why the connection ended. It returns nil for a clean close.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Close ends the management session. It does not stop openvpn; use Disconnect
// for that.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.conn.Close()
}

func (c *Client) readLoop() {
	defer func() {
		c.closeOnce.Do(func() { close(c.closed) })
		close(c.events)
	}()

	for {
		unit, err := c.readUnit()
		if err != nil {
			c.errMu.Lock()
			select {
			case <-c.closed: // expected: we asked to close
			default:
				c.err = err
			}
			c.errMu.Unlock()
			return
		}
		if unit == "" {
			continue
		}
		c.trace("<-", unit)

		// Lines starting with ">" are unsolicited notifications; everything
		// else belongs to whichever command is in flight.
		if strings.HasPrefix(unit, ">") {
			c.emit(parse(unit))
			continue
		}
		select {
		case c.replies <- unit:
		default:
			// No waiter and no room: a stale reply. Surface it as an event so
			// it is at least visible in the log rather than silently lost.
			c.emit(parse(unit))
		}
	}
}

// emit delivers a notification, dropping high-volume telemetry rather than
// stalling the read loop if the consumer falls behind. State changes, errors
// and credential prompts are never dropped.
func (c *Client) emit(ev Event) {
	switch ev.Kind {
	case KindLog, KindByteCount, KindEcho:
		select {
		case c.events <- ev:
		default:
		}
	default:
		select {
		case c.events <- ev:
		case <-c.closed:
		}
	}
}

// readUnit reads one line, or the newline-less password prompt.
func (c *Client) readUnit() (string, error) {
	c.line.Reset()
	for {
		b, err := c.br.ReadByte()
		if err != nil {
			return c.line.String(), err
		}
		if b == '\n' {
			return strings.TrimRight(c.line.String(), "\r"), nil
		}
		c.line.WriteByte(b)
		// The prompt is the only message without a newline. Only test for it
		// when a colon lands, to keep this off the hot path for log lines.
		if b == ':' && c.line.Len() == len(passwordPrompt) &&
			bytes.Equal(c.line.Bytes(), []byte(passwordPrompt)) {
			return passwordPrompt, nil
		}
	}
}

// write puts one line on the wire.
func (c *Client) write(line string) error {
	return c.writeLines(line)
}

// writeLines puts several lines on the wire in a single write.
//
// Batching matters more than it looks. openvpn calls
// buffer_list_reset(man->connection.out) the moment input arrives, discarding
// anything it had queued but not yet flushed -- including a >PASSWORD: prompt.
// Every separate write is another chance to destroy a notification, so a
// logically single action like "here are my credentials" must reach openvpn as
// one read.
func (c *Client) writeLines(lines ...string) error {
	if len(lines) == 0 {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var payload strings.Builder
	for _, line := range lines {
		c.trace("->", line)
		payload.WriteString(line)
		payload.WriteByte('\n')
	}
	if _, err := c.conn.Write([]byte(payload.String())); err != nil {
		return fmt.Errorf("write %q: %w", firstWord(lines[0]), err)
	}
	return nil
}

// Command sends a command that openvpn answers with a single status line, and
// waits for that acknowledgement.
//
// An error carries openvpn's own message, which is usually worth showing to the
// user verbatim.
func (c *Client) Command(line string) error {
	_, err := c.exchange(line, false)
	return err
}

// Query sends a command that returns a payload, and returns the payload lines.
//
// The distinction from Command is not cosmetic. Payload-returning commands
// ("state", "log", "status", and the "on all" subscribe-plus-history forms)
// answer with the status line FIRST, then the payload, then a bare "END". A
// reader that stops at the status line leaves the payload queued and every
// later command reads the previous command's leftovers.
func (c *Client) Query(line string) ([]string, error) {
	return c.exchange(line, true)
}

// exchange runs one command/reply cycle. waitForEnd selects the terminator.
func (c *Client) exchange(line string, waitForEnd bool) ([]string, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	c.drainReplies()

	if err := c.write(line); err != nil {
		return nil, err
	}

	var payload []string
	timeout := time.After(commandTimeout)
	for {
		select {
		case reply := <-c.replies:
			switch {
			case reply == "END":
				return payload, nil
			case strings.HasPrefix(reply, "ERROR:"):
				return payload, fmt.Errorf("%s: %s", firstWord(line), strings.TrimPrefix(reply, "ERROR: "))
			case strings.HasPrefix(reply, "SUCCESS:"):
				if !waitForEnd {
					return payload, nil
				}
				// Payload and "END" still to come.
			default:
				payload = append(payload, reply)
			}
		case <-c.closed:
			return payload, fmt.Errorf("%s: management connection closed", firstWord(line))
		case <-timeout:
			return payload, fmt.Errorf("%s: no reply within %s", firstWord(line), commandTimeout)
		}
	}
}

// drainReplies discards anything left over from an earlier exchange, so it
// cannot be mistaken for the next command's reply.
func (c *Client) drainReplies() {
	for {
		select {
		case <-c.replies:
		default:
			return
		}
	}
}

// batch sends several single-reply commands as one write and waits for all of
// their acknowledgements.
//
// Use this whenever two commands form one action. See writeLines for why the
// number of writes is what matters.
func (c *Client) batch(lines ...string) error {
	if len(lines) == 0 {
		return nil
	}
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	c.drainReplies()

	if err := c.writeLines(lines...); err != nil {
		return err
	}

	remaining := len(lines)
	timeout := time.After(commandTimeout)
	for remaining > 0 {
		select {
		case reply := <-c.replies:
			switch {
			case strings.HasPrefix(reply, "ERROR:"):
				return fmt.Errorf("%s: %s", firstWord(lines[0]), strings.TrimPrefix(reply, "ERROR: "))
			case strings.HasPrefix(reply, "SUCCESS:"):
				remaining--
			}
		case <-c.closed:
			return fmt.Errorf("%s: management connection closed", firstWord(lines[0]))
		case <-timeout:
			return fmt.Errorf("%s: no reply within %s", firstWord(lines[0]), commandTimeout)
		}
	}
	return nil
}

// --- commands -------------------------------------------------------------

// StateOn enables ">STATE:" notifications and returns the state history.
//
// The "on all" form is deliberate. openvpn emits its first state transitions
// while still initialising, and any notification still queued when our next
// command arrives is discarded -- openvpn clears its pending output on every
// read. Taking the history in the same exchange closes that gap, and State can
// be used to re-synchronise later.
func (c *Client) StateOn() ([]string, error) {
	return c.Query("state on all")
}

// State returns the state history without changing the subscription. Use it to
// re-synchronise after sending commands, rather than trusting that no
// notification was dropped.
func (c *Client) State() ([]string, error) {
	return c.Query("state")
}

// LogOn enables real-time ">LOG:" notifications.
//
// It deliberately does not ask for history: at high verbosity openvpn's cache
// is hundreds of lines of option dump, and the log file has everything anyway.
func (c *Client) LogOn() error {
	return c.Command("log on")
}

// LogHistory returns the most recent n cached log lines.
func (c *Client) LogHistory(n int) ([]string, error) {
	return c.Query(fmt.Sprintf("log %d", n))
}

// ByteCount enables ">BYTECOUNT:" notifications every interval seconds. Pass
// zero to turn them off.
func (c *Client) ByteCount(interval int) error {
	return c.Command(fmt.Sprintf("bytecount %d", interval))
}

// HoldRelease starts the tunnel. Required when openvpn was launched with
// --management-hold, which is what gives us a chance to subscribe to
// notifications before anything happens.
func (c *Client) HoldRelease() error {
	return c.Command("hold release")
}

// Status returns openvpn's status dump, which includes the routing table and
// per-connection statistics.
func (c *Client) Status() ([]string, error) {
	return c.Query("status")
}

// Credentials answers a username-and-password prompt.
//
// Both lines go out in one write on purpose. Sent separately, the second write
// can land while openvpn has already queued its next question -- typically the
// private key passphrase -- and openvpn discards queued output when input
// arrives. The prompt is then lost, openvpn waits for an answer that will never
// come, and the connection hangs with nothing to show the user.
func (c *Client) Credentials(realm, username, password string) error {
	return c.batch(
		"username "+quote(realm)+" "+quote(username),
		"password "+quote(realm)+" "+quote(password),
	)
}

// Password answers a prompt that only wants a passphrase, such as a private key.
func (c *Client) Password(realm, password string) error {
	return c.Command("password " + quote(realm) + " " + quote(password))
}

// AnswerStaticChallenge replies to a prompt that carried "SC:", packing the
// password and the second factor into one value.
func (c *Client) AnswerStaticChallenge(realm, username, password, response string) error {
	return c.Credentials(realm, username, "SCRV1:"+b64(password)+":"+b64(response))
}

// AnswerDynamicChallenge replies to a CRV1 challenge issued after a failed
// verification. The state id must be echoed back verbatim.
func (c *Client) AnswerDynamicChallenge(realm, username, stateID, response string) error {
	return c.Credentials(realm, username, "CRV1::"+stateID+"::"+response)
}

// CRResponse sends a challenge response over the dedicated channel available in
// OpenVPN 2.5 and later.
func (c *Client) CRResponse(response string) error {
	return c.Command("cr-response " + b64(response))
}

// NeedOK confirms or cancels a ">NEED-OK:" request, such as a token-insertion
// prompt.
func (c *Client) NeedOK(kind string, ok bool) error {
	answer := "cancel"
	if ok {
		answer = "ok"
	}
	return c.Command("needok " + kind + " " + answer)
}

// NeedStr answers a ">NEED-STR:" request.
func (c *Client) NeedStr(name, value string) error {
	return c.Command("needstr " + name + " " + quote(value))
}

// Signal sends a signal to openvpn. SIGTERM disconnects, SIGUSR1 forces a
// reconnect, SIGHUP restarts with a fresh config read.
func (c *Client) Signal(sig string) error {
	return c.Command("signal " + sig)
}

// Disconnect asks openvpn to shut down cleanly.
func (c *Client) Disconnect() error { return c.Signal("SIGTERM") }

// --- helpers --------------------------------------------------------------

// quote renders s as a management-interface string argument. Values containing
// spaces must be quoted, and backslashes and quotes escaped.
func quote(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	sb.WriteByte('"')
	return sb.String()
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func decodeBase64(s string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return string(b)
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}
