package mgmt

import "testing"

// The lines here are transcripts captured from a real openvpn 2.5.5 management
// session, plus the credential shapes documented in doc/management-notes.txt.
// They are verbatim on purpose: this parser's whole job is to agree with what
// openvpn actually emits, not with what it ought to.

func TestParseStateNotification(t *testing.T) {
	ev := parse(">STATE:1787182927,CONNECTED,SUCCESS,10.99.0.2,127.0.0.1,55216,,")
	if ev.Kind != KindState {
		t.Fatalf("Kind = %q, want %q", ev.Kind, KindState)
	}
	if ev.State != StateConnected {
		t.Errorf("State = %q, want %q", ev.State, StateConnected)
	}
	if ev.Detail != "SUCCESS" {
		t.Errorf("Detail = %q", ev.Detail)
	}
	if ev.LocalIP != "10.99.0.2" {
		t.Errorf("LocalIP = %q", ev.LocalIP)
	}
	if ev.RemoteIP != "127.0.0.1" || ev.RemotePort != "55216" {
		t.Errorf("remote = %s:%s", ev.RemoteIP, ev.RemotePort)
	}
	if ev.Time.IsZero() {
		t.Error("Time was not decoded")
	}
}

func TestParseStateWithEmptyTrailingFields(t *testing.T) {
	// Most transitions carry almost nothing; reading by index must not panic.
	ev := parse(">STATE:1787182611,ASSIGN_IP,,10.99.0.2,,,,")
	if ev.State != StateAssignIP {
		t.Errorf("State = %q, want %q", ev.State, StateAssignIP)
	}
	if ev.LocalIP != "10.99.0.2" {
		t.Errorf("LocalIP = %q", ev.LocalIP)
	}
	if ev.RemoteIP != "" {
		t.Errorf("RemoteIP = %q, want empty", ev.RemoteIP)
	}
}

func TestParseStateLineFromHistory(t *testing.T) {
	// "state on all" returns the same fields without the ">STATE:" prefix.
	ev := ParseStateLine("1787182890,CONNECTING,,,,,,")
	if ev.Kind != KindState {
		t.Errorf("Kind = %q", ev.Kind)
	}
	if ev.State != StateConnecting {
		t.Errorf("State = %q, want %q", ev.State, StateConnecting)
	}
}

func TestParseByteCount(t *testing.T) {
	ev := parse(">BYTECOUNT:1252,5544")
	if ev.Kind != KindByteCount {
		t.Fatalf("Kind = %q", ev.Kind)
	}
	if ev.BytesIn != 1252 || ev.BytesOut != 5544 {
		t.Errorf("in/out = %d/%d, want 1252/5544", ev.BytesIn, ev.BytesOut)
	}
}

func TestParseLogKeepsCommasInMessage(t *testing.T) {
	ev := parse(">LOG:1787182611,I,Notified TAP-Windows driver to set a DHCP IP/netmask of 10.99.0.2/255.255.255.252, lease-time: 31536000")
	if ev.Kind != KindLog {
		t.Fatalf("Kind = %q", ev.Kind)
	}
	if ev.Level != "I" {
		t.Errorf("Level = %q", ev.Level)
	}
	want := "Notified TAP-Windows driver to set a DHCP IP/netmask of 10.99.0.2/255.255.255.252, lease-time: 31536000"
	if ev.Text != want {
		t.Errorf("Text = %q\nwant %q", ev.Text, want)
	}
}

func TestParseLogWithEmptyLevel(t *testing.T) {
	// openvpn leaves the flags field empty for plenty of lines.
	ev := parse(">LOG:1787182611,,do_ifconfig, ipv4=1, ipv6=0")
	if ev.Level != "" {
		t.Errorf("Level = %q, want empty", ev.Level)
	}
	if ev.Text != "do_ifconfig, ipv4=1, ipv6=0" {
		t.Errorf("Text = %q", ev.Text)
	}
}

func TestParseHoldAndInfo(t *testing.T) {
	if ev := parse(">HOLD:Waiting for hold release:0"); ev.Kind != KindHold {
		t.Errorf("hold Kind = %q", ev.Kind)
	}
	ev := parse(">INFO:OpenVPN Management Interface Version 3 -- type 'help' for more info")
	if ev.Kind != KindInfo {
		t.Errorf("info Kind = %q", ev.Kind)
	}
	if ev.Text == "" {
		t.Error("info Text is empty")
	}
}

func TestParseFatal(t *testing.T) {
	ev := parse(">FATAL:All TAP-Windows adapters on this system are currently in use.")
	if ev.Kind != KindFatal {
		t.Fatalf("Kind = %q", ev.Kind)
	}
	if ev.Text != "All TAP-Windows adapters on this system are currently in use." {
		t.Errorf("Text = %q", ev.Text)
	}
}

func TestParseCommandReply(t *testing.T) {
	ev := parse("SUCCESS: real-time state notification set to ON")
	if ev.Kind != KindReply {
		t.Errorf("Kind = %q, want %q for a non-notification line", ev.Kind, KindReply)
	}
}

func TestParsePasswordPrompts(t *testing.T) {
	tests := []struct {
		name string
		line string
		want PasswordRequest
	}{
		{
			name: "username and password",
			line: ">PASSWORD:Need 'Auth' username/password",
			want: PasswordRequest{Realm: "Auth", NeedUsername: true},
		},
		{
			name: "private key passphrase only",
			line: ">PASSWORD:Need 'Private Key' password",
			want: PasswordRequest{Realm: "Private Key"},
		},
		{
			name: "static challenge, response may be echoed",
			line: ">PASSWORD:Need 'Auth' username/password SC:1,Enter your token code",
			want: PasswordRequest{
				Realm: "Auth", NeedUsername: true,
				StaticChallenge: "Enter your token code", StaticChallengeEcho: true,
			},
		},
		{
			name: "static challenge, response hidden",
			line: ">PASSWORD:Need 'Auth' username/password SC:0,PIN",
			want: PasswordRequest{
				Realm: "Auth", NeedUsername: true,
				StaticChallenge: "PIN", StaticChallengeEcho: false,
			},
		},
		{
			name: "plain rejection",
			line: ">PASSWORD:Verification Failed: 'Auth'",
			want: PasswordRequest{Realm: "Auth", Failed: true},
		},
		{
			name: "rejection with a dynamic challenge",
			line: ">PASSWORD:Verification Failed: 'Auth' ['CRV1:R,E:state-7:YWxpY2U=:Enter the code from your token']",
			want: PasswordRequest{
				Realm: "Auth", Failed: true,
				ChallengeStateID: "state-7",
				ChallengeUser:    "alice",
				DynamicChallenge: "Enter the code from your token",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := parse(tc.line)
			if ev.Kind != KindPassword {
				t.Fatalf("Kind = %q, want %q", ev.Kind, KindPassword)
			}
			got := ev.Password
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestQuoteEscapesForTheWire(t *testing.T) {
	// Management arguments are whitespace-separated unless quoted, and quotes
	// and backslashes inside need escaping, or a password with a space in it
	// silently becomes two arguments.
	for in, want := range map[string]string{
		"simple":         `"simple"`,
		"two words":      `"two words"`,
		`has"quote`:      `"has\"quote"`,
		`back\slash`:     `"back\\slash"`,
		"":               `""`,
		`both"\together`: `"both\"\\together"`,
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDecodeBase64IsForgiving(t *testing.T) {
	if got := decodeBase64("YWxpY2U="); got != "alice" {
		t.Errorf("decodeBase64 = %q, want alice", got)
	}
	// A malformed challenge must not take the connection down.
	if got := decodeBase64("not base64!!"); got != "" {
		t.Errorf("decodeBase64 on garbage = %q, want empty", got)
	}
}
