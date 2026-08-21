// Package mgmt is a client for the OpenVPN management interface.
//
// Everything the UI needs about a running tunnel arrives over this one
// connection: state transitions, throughput, log lines, and credential
// prompts. Reference: openvpn/doc/management-notes.txt
package mgmt

import (
	"strconv"
	"strings"
	"time"
)

// Kind identifies a real-time notification from openvpn. These arrive
// unsolicited on lines prefixed with ">".
type Kind string

const (
	KindState     Kind = "state"
	KindByteCount Kind = "bytecount"
	KindLog       Kind = "log"
	KindEcho      Kind = "echo"
	KindHold      Kind = "hold"
	KindPassword  Kind = "password"
	KindNeedOK    Kind = "need-ok"
	KindNeedStr   Kind = "need-str"
	KindInfo      Kind = "info"
	KindFatal     Kind = "fatal"
	KindClient    Kind = "client"
	// KindReply is a synchronous response to a command we sent
	// ("SUCCESS: ...", "ERROR: ...", or a payload line).
	KindReply Kind = "reply"
	// KindUnknown is a notification we do not model yet.
	KindUnknown Kind = "unknown"
)

// State is the connection state reported by ">STATE:". These are openvpn's own
// names; the UI maps them onto a smaller, friendlier set.
type State string

const (
	StateConnecting   State = "CONNECTING"
	StateWait         State = "WAIT"
	StateAuth         State = "AUTH"
	StateGetConfig    State = "GET_CONFIG"
	StateAssignIP     State = "ASSIGN_IP"
	StateAddRoutes    State = "ADD_ROUTES"
	StateConnected    State = "CONNECTED"
	StateReconnecting State = "RECONNECTING"
	StateExiting      State = "EXITING"
	StateResolve      State = "RESOLVE"
	StateTCPConnect   State = "TCP_CONNECT"
)

// Event is one decoded message from the management interface.
type Event struct {
	Kind Kind
	Raw  string

	// Time is the openvpn-supplied timestamp, when the message carries one.
	Time time.Time

	// State is set for KindState.
	State State
	// Detail is the state's description field, e.g. "SUCCESS" or an error
	// reason such as "auth-failure".
	Detail string
	// LocalIP is the address assigned to the tunnel interface.
	LocalIP string
	// RemoteIP and RemotePort identify the server we reached.
	RemoteIP   string
	RemotePort string

	// BytesIn and BytesOut are set for KindByteCount, cumulative per session.
	BytesIn  uint64
	BytesOut uint64

	// Level is the openvpn log severity flag for KindLog:
	// I(nfo), F(atal), N(on-fatal), W(arning), D(ebug).
	Level string
	// Text is the message body: the log line, the prompt, the reply.
	Text string

	// Password is set for KindPassword.
	Password PasswordRequest
}

// PasswordRequest describes what openvpn is asking us for. The three shapes we
// must handle are a plain credential prompt, a static challenge (2FA presented
// up front alongside the password), and a dynamic challenge (2FA demanded after
// the first password was accepted-but-insufficient).
type PasswordRequest struct {
	// Realm is the credential scope: "Auth", "Private Key", "HTTP Proxy", ...
	Realm string
	// NeedUsername is false for realms that only want a passphrase, such as
	// "Private Key".
	NeedUsername bool

	// Failed marks ">PASSWORD:Verification Failed", i.e. the server rejected
	// what we sent.
	Failed bool
	// Reason carries the server's explanation on failure, when it gave one.
	Reason string

	// StaticChallenge is set when the server asked for an extra factor in the
	// same exchange as the password (SC:<echo>,<text>).
	StaticChallenge string
	// StaticChallengeEcho reports whether the response may be shown on screen.
	StaticChallengeEcho bool

	// DynamicChallenge is set when the server issued a CRV1 challenge.
	DynamicChallenge string
	// ChallengeStateID and ChallengeUser come from the CRV1 payload and must be
	// echoed back in the response.
	ChallengeStateID string
	ChallengeUser    string
}

// parse decodes one raw line from openvpn into an Event.
func parse(line string) Event {
	ev := Event{Raw: line}

	if !strings.HasPrefix(line, ">") {
		ev.Kind = KindReply
		ev.Text = line
		return ev
	}

	head, body, _ := strings.Cut(strings.TrimPrefix(line, ">"), ":")
	switch head {
	case "STATE":
		ev.Kind = KindState
		parseState(&ev, body)
	case "BYTECOUNT":
		ev.Kind = KindByteCount
		inStr, outStr, _ := strings.Cut(body, ",")
		ev.BytesIn, _ = strconv.ParseUint(strings.TrimSpace(inStr), 10, 64)
		ev.BytesOut, _ = strconv.ParseUint(strings.TrimSpace(outStr), 10, 64)
	case "LOG":
		ev.Kind = KindLog
		parseLog(&ev, body)
	case "ECHO":
		ev.Kind = KindEcho
		parseLog(&ev, body)
	case "HOLD":
		ev.Kind = KindHold
		ev.Text = body
	case "PASSWORD":
		ev.Kind = KindPassword
		ev.Text = body
		ev.Password = parsePassword(body)
	case "NEED-OK":
		ev.Kind = KindNeedOK
		ev.Text = body
	case "NEED-STR":
		ev.Kind = KindNeedStr
		ev.Text = body
	case "INFO":
		ev.Kind = KindInfo
		ev.Text = body
	case "FATAL":
		ev.Kind = KindFatal
		ev.Text = body
	case "CLIENT":
		ev.Kind = KindClient
		ev.Text = body
	default:
		ev.Kind = KindUnknown
		ev.Text = body
	}
	return ev
}

// ParseStateLine decodes a bare state line, as returned in the payload of
// "state" and "state on all". It carries the same comma-separated fields as a
// ">STATE:" notification but without the prefix, so history and live events can
// be handled by one code path.
func ParseStateLine(line string) Event {
	ev := Event{Kind: KindState, Raw: line}
	parseState(&ev, strings.TrimSpace(line))
	return ev
}

// parseState decodes the comma-separated ">STATE:" payload:
//
//	<unix time>,<state>,<detail>,<local ip>,<remote ip>,<remote port>,...
//
// Trailing fields are frequently empty, so read defensively by index.
func parseState(ev *Event, body string) {
	f := strings.Split(body, ",")
	at := func(i int) string {
		if i < len(f) {
			return strings.TrimSpace(f[i])
		}
		return ""
	}
	ev.Time = parseUnix(at(0))
	ev.State = State(at(1))
	ev.Detail = at(2)
	ev.LocalIP = at(3)
	ev.RemoteIP = at(4)
	ev.RemotePort = at(5)
}

// parseLog decodes "<unix time>,<flags>,<message>". The message itself may
// contain commas, so split only twice.
func parseLog(ev *Event, body string) {
	f := strings.SplitN(body, ",", 3)
	if len(f) > 0 {
		ev.Time = parseUnix(f[0])
	}
	if len(f) > 1 {
		ev.Level = f[1]
	}
	if len(f) > 2 {
		ev.Text = f[2]
	} else {
		ev.Text = body
	}
}

// parsePassword decodes the several shapes of ">PASSWORD:".
func parsePassword(body string) PasswordRequest {
	var req PasswordRequest

	if rest, ok := cutPrefix(body, "Verification Failed:"); ok {
		req.Failed = true
		rest = strings.TrimSpace(rest)
		req.Realm = firstQuoted(rest)

		// A dynamic challenge rides along in square brackets:
		//   'Auth' ['CRV1:<flags>:<state>:<user b64>:<challenge>']
		if open := strings.Index(rest, "["); open >= 0 {
			inner := strings.Trim(rest[open:], "[]")
			inner = strings.Trim(inner, "'")
			if crv, ok := cutPrefix(inner, "CRV1:"); ok {
				parseCRV1(&req, crv)
			} else {
				req.Reason = inner
			}
		}
		return req
	}

	// "Need 'Auth' username/password" or "Need 'Private Key' password",
	// optionally suffixed with " SC:<echo>,<text>" for a static challenge.
	rest, ok := cutPrefix(body, "Need ")
	if !ok {
		req.Realm = firstQuoted(body)
		return req
	}
	req.Realm = firstQuoted(rest)
	req.NeedUsername = strings.Contains(rest, "username")

	if sc := strings.Index(rest, "SC:"); sc >= 0 {
		echo, text, _ := strings.Cut(rest[sc+len("SC:"):], ",")
		req.StaticChallenge = text
		req.StaticChallengeEcho = strings.TrimSpace(echo) == "1"
	}
	return req
}

// parseCRV1 decodes "<flags>:<state id>:<username b64>:<challenge text>".
func parseCRV1(req *PasswordRequest, s string) {
	f := strings.SplitN(s, ":", 4)
	if len(f) > 1 {
		req.ChallengeStateID = f[1]
	}
	if len(f) > 2 {
		req.ChallengeUser = decodeBase64(f[2])
	}
	if len(f) > 3 {
		req.DynamicChallenge = f[3]
	}
}

// firstQuoted pulls the first single-quoted token out of a prompt.
func firstQuoted(s string) string {
	open := strings.Index(s, "'")
	if open < 0 {
		return ""
	}
	rest := s[open+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

func parseUnix(s string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
