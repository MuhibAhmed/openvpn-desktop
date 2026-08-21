package vpn

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOpenVPN is a management interface that behaves the way openvpn does,
// including the parts that are easy to get wrong: the newline-less password
// prompt, the status-line-then-payload-then-END reply shape, and unsolicited
// notifications interleaved with command replies.
//
// It exists so the connection state machine, credential handling and error
// translation can be tested without a VPN, a server, or admin rights.
type fakeOpenVPN struct {
	password string

	// script runs once the client releases the hold. It is where a test decides
	// what "openvpn" does next.
	script func(s *fakeOpenVPN)

	mu        sync.Mutex
	conn      net.Conn
	received  []string
	scriptRan bool
	closed    bool

	listener net.Listener
	ready    chan struct{}
	done     chan struct{}
}

func newFakeOpenVPN(t *testing.T, port int, password string, script func(*fakeOpenVPN)) *fakeOpenVPN {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on the port the session picked: %v", err)
	}
	f := &fakeOpenVPN{
		password: password,
		script:   script,
		listener: ln,
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
	}
	go f.serve()
	t.Cleanup(f.close)
	return f
}

func (f *fakeOpenVPN) serve() {
	defer close(f.done)
	conn, err := f.listener.Accept()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	close(f.ready)

	// openvpn writes this prompt with no trailing newline.
	fmt.Fprint(conn, "ENTER PASSWORD:")

	r := bufio.NewReader(conn)
	first, err := r.ReadString('\n')
	if err != nil {
		return
	}
	if strings.TrimSpace(first) != f.password {
		f.send("ERROR: bad password")
		conn.Close()
		return
	}
	f.send("SUCCESS: password is correct")
	f.send(">INFO:OpenVPN Management Interface Version 3 -- type 'help' for more info")
	// openvpn is started with --management-hold, and re-announces the hold to
	// every management client that connects. Leaving this out of the fake is what
	// let a missing hold release ship.
	f.send(">HOLD:Waiting for hold release:0")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f.handle(strings.TrimSpace(line))
	}
}

func (f *fakeOpenVPN) handle(cmd string) {
	f.mu.Lock()
	f.received = append(f.received, cmd)
	f.mu.Unlock()

	switch {
	case cmd == "state on all":
		// Status line first, then the payload, then END. Getting this order
		// wrong is exactly the bug this shape is here to catch.
		f.send("SUCCESS: real-time state notification set to ON")
		f.send("1700000000,CONNECTING,,,,,,")
		f.send("END")
	case cmd == "state":
		f.send("SUCCESS: state")
		f.send("1700000000,CONNECTING,,,,,,")
		f.send("END")
	case cmd == "log on":
		f.send("SUCCESS: real-time log notification set to ON")
	case strings.HasPrefix(cmd, "bytecount"):
		f.send("SUCCESS: bytecount interval changed")
	case cmd == "hold release":
		f.send("SUCCESS: hold release succeeded")
		// Only the first release starts the script. A later release is openvpn
		// retrying after a restart, and replaying the whole script there would
		// loop forever.
		if f.script != nil && !f.scriptRan {
			f.scriptRan = true
			go f.script(f)
		}
	case cmd == "signal SIGTERM":
		f.send("SUCCESS: signal SIGTERM thrown")
		f.notify(">STATE:1700000009,EXITING,SIGTERM,,,,,")
		f.close()
	default:
		f.send("SUCCESS: " + strings.Fields(cmd)[0])
	}
}

// send writes one line to the client.
func (f *fakeOpenVPN) send(line string) {
	f.mu.Lock()
	conn, closed := f.conn, f.closed
	f.mu.Unlock()
	if conn == nil || closed {
		return
	}
	fmt.Fprintf(conn, "%s\r\n", line)
}

// notify is send, named for readability at call sites that push events.
func (f *fakeOpenVPN) notify(line string) { f.send(line) }

func (f *fakeOpenVPN) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.received...)
}

// sawCommandContaining reports whether any command received so far contains sub.
func (f *fakeOpenVPN) sawCommandContaining(sub string) bool {
	for _, c := range f.commands() {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func (f *fakeOpenVPN) close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	conn := f.conn
	f.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	f.listener.Close()
}

// fakeRunner stands in for the platform launcher. Instead of starting openvpn it
// starts a fakeOpenVPN on the port the session chose.
type fakeRunner struct {
	script    func(*fakeOpenVPN)
	t         *testing.T
	startErr  error
	preflight error

	mu    sync.Mutex
	fake  *fakeOpenVPN
	procs []*fakeProcess
}

func (r *fakeRunner) Preflight() error { return r.preflight }

func (r *fakeRunner) Start(ctx context.Context, req StartRequest) (Process, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}
	fake := newFakeOpenVPN(r.t, req.ManagementPort, req.ManagementPassword, r.script)
	proc := &fakeProcess{done: make(chan struct{}), fake: fake}
	// The real launcher learns of death when the service drops the pipe; here
	// the fake server closing stands in for that.
	go func() {
		<-fake.done
		proc.markDone()
	}()

	r.mu.Lock()
	r.fake = fake
	r.procs = append(r.procs, proc)
	r.mu.Unlock()
	return proc, nil
}

func (r *fakeRunner) server() *fakeOpenVPN {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fake
}

type fakeProcess struct {
	fake *fakeOpenVPN
	once sync.Once
	done chan struct{}

	mu      sync.Mutex
	stopped bool
}

func (p *fakeProcess) PID() int              { return 4242 }
func (p *fakeProcess) Done() <-chan struct{} { return p.done }
func (p *fakeProcess) Stop() error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.fake.close()
	return nil
}
func (p *fakeProcess) Close() error { return nil }
func (p *fakeProcess) markDone()    { p.once.Do(func() { close(p.done) }) }

// recorder collects published statuses so tests can wait on a condition rather
// than sleeping and hoping.
type recorder struct {
	mu       sync.Mutex
	statuses []Status
	logs     []LogLine
	changed  chan struct{}
}

func newRecorder() *recorder {
	return &recorder{changed: make(chan struct{}, 256)}
}

func (r *recorder) handlers() Handlers {
	return Handlers{
		OnStatus: func(s Status) {
			r.mu.Lock()
			r.statuses = append(r.statuses, s)
			r.mu.Unlock()
			select {
			case r.changed <- struct{}{}:
			default:
			}
		},
		OnLog: func(l LogLine) {
			r.mu.Lock()
			r.logs = append(r.logs, l)
			r.mu.Unlock()
		},
	}
}

func (r *recorder) latest() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.statuses) == 0 {
		return Status{}
	}
	return r.statuses[len(r.statuses)-1]
}

// await waits for a status satisfying cond, returning it, or fails the test.
func (r *recorder) await(t *testing.T, what string, cond func(Status) bool) Status {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		for _, s := range r.statuses {
			if cond(s) {
				r.mu.Unlock()
				return s
			}
		}
		r.mu.Unlock()

		select {
		case <-r.changed:
		case <-deadline:
			r.mu.Lock()
			seen := make([]string, 0, len(r.statuses))
			for _, s := range r.statuses {
				seen = append(seen, fmt.Sprintf("%s(%q)", s.Phase, s.Detail))
			}
			r.mu.Unlock()
			t.Fatalf("timed out waiting for %s; saw %s", what, strings.Join(seen, " -> "))
		}
	}
}

func startSession(t *testing.T, script func(*fakeOpenVPN)) (*Session, *fakeRunner, *recorder) {
	t.Helper()
	runner := &fakeRunner{script: script, t: t}
	rec := newRecorder()
	session := newSession(runner, SessionConfig{
		ProfileID:   "test",
		ProfileName: "Test Profile",
		ConfigPath:  t.TempDir() + "/test.ovpn",
		LogPath:     t.TempDir() + "/test.log",
	}, rec.handlers())

	if err := session.start(context.Background(), 3); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { session.Disconnect() })
	return session, runner, rec
}

func TestSessionReachesConnected(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">LOG:1700000001,I,TCP/UDP: Preserving recently used remote address")
		f.notify(">STATE:1700000002,AUTH,,,,,,")
		f.notify(">STATE:1700000003,GET_CONFIG,,,,,,")
		f.notify(">STATE:1700000004,ASSIGN_IP,,10.8.0.6,,,,")
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
		f.notify(">BYTECOUNT:2048,4096")
	})

	got := rec.await(t, "connected", func(s Status) bool { return s.Phase == PhaseConnected })
	if got.Detail != "Connected" {
		t.Errorf("Detail = %q, want %q", got.Detail, "Connected")
	}
	if got.LocalIP != "10.8.0.6" {
		t.Errorf("LocalIP = %q, want 10.8.0.6", got.LocalIP)
	}
	if got.RemoteIP != "203.0.113.9" || got.RemotePort != "1194" {
		t.Errorf("remote = %s:%s, want 203.0.113.9:1194", got.RemoteIP, got.RemotePort)
	}
	if got.ConnectedAt.IsZero() {
		t.Error("ConnectedAt is zero on a connected session")
	}

	rec.await(t, "byte counters", func(s Status) bool { return s.BytesIn == 2048 && s.BytesOut == 4096 })

	// The intermediate phases must be reported, not skipped, or the UI has
	// nothing to show while connecting.
	var sawAuth, sawConfiguring bool
	rec.mu.Lock()
	for _, s := range rec.statuses {
		switch s.Phase {
		case PhaseAuthenticating:
			sawAuth = true
		case PhaseConfiguring:
			sawConfiguring = true
		}
	}
	rec.mu.Unlock()
	if !sawAuth || !sawConfiguring {
		t.Errorf("intermediate phases missing: auth=%v configuring=%v", sawAuth, sawConfiguring)
	}

	// Subscriptions must be set up before the hold is released, or the early
	// transitions are lost.
	cmds := runner.server().commands()
	holdAt, stateAt := indexOf(cmds, "hold release"), indexOf(cmds, "state on all")
	if stateAt < 0 || holdAt < 0 || stateAt > holdAt {
		t.Errorf("commands = %v, want state subscription before hold release", cmds)
	}

	if logs := session.Logs(); len(logs) == 0 {
		t.Error("no log lines captured")
	}
}

func TestSessionPromptsForCredentialsAndSendsThem(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000002,AUTH,,,,,,")
		f.notify(">PASSWORD:Need 'Auth' username/password")
	})

	st := rec.await(t, "credential prompt", func(s Status) bool { return s.Prompt != nil })
	if st.Prompt.Kind != PromptCredentials {
		t.Fatalf("Kind = %q, want %q", st.Prompt.Kind, PromptCredentials)
	}
	if st.Prompt.Realm != "Auth" {
		t.Errorf("Realm = %q, want Auth", st.Prompt.Realm)
	}
	if st.Phase != PhaseAuthenticating {
		t.Errorf("Phase = %q while prompting, want %q", st.Phase, PhaseAuthenticating)
	}

	if err := session.Answer(Answer{PromptID: st.Prompt.ID, Username: "alice", Password: "s3cret"}); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	awaitCommand(t, runner.server(), `username "Auth" "alice"`)
	awaitCommand(t, runner.server(), `password "Auth" "s3cret"`)

	// The prompt must be cleared once answered, or the dialog stays up.
	rec.await(t, "prompt cleared", func(s Status) bool { return s.Prompt == nil && s.Phase == PhaseAuthenticating })
}

// TestSessionAsksForKeyPassphraseAfterCredentials covers the sequence a real
// server produces: username and password first, then the private key
// passphrase. Both prompts have to reach the user.
func TestSessionAsksForKeyPassphraseAfterCredentials(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000002,AUTH,,,,,,")
		f.notify(">PASSWORD:Need 'Auth' username/password")
	})

	first := rec.await(t, "credential prompt", func(s Status) bool { return s.Prompt != nil })
	if err := session.Answer(Answer{
		PromptID: first.Prompt.ID, Username: "alice", Password: "s3cret",
	}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	awaitCommand(t, runner.server(), `password "Auth" "s3cret"`)

	// openvpn now asks for the key passphrase. Missing this is what makes a
	// connection hang with no error at all.
	runner.server().notify(">PASSWORD:Need 'Private Key' password")

	second := rec.await(t, "passphrase prompt", func(s Status) bool {
		return s.Prompt != nil && s.Prompt.Kind == PromptPassphrase
	})
	if second.Prompt.Realm != "Private Key" {
		t.Errorf("Realm = %q, want %q", second.Prompt.Realm, "Private Key")
	}
	if second.Prompt.ID == first.Prompt.ID {
		t.Error("the second prompt reused the first prompt's id, so answering it would be rejected as stale")
	}

	if err := session.Answer(Answer{
		PromptID: second.Prompt.ID, Password: "keypass",
	}); err != nil {
		t.Fatalf("Answer passphrase: %v", err)
	}
	awaitCommand(t, runner.server(), `password "Private Key" "keypass"`)

	// A passphrase prompt must not send a username: openvpn is not asking for
	// one and the extra command is another chance to destroy a queued prompt.
	for _, cmd := range runner.server().commands() {
		if strings.HasPrefix(cmd, `username "Private Key"`) {
			t.Errorf("sent %q, but the prompt only wanted a passphrase", cmd)
		}
	}
}

// TestSessionSendsNothingElseWhenAnsweringCredentials pins down the bug that
// made real connections hang: openvpn discards queued output the moment input
// arrives, so any command we send after the credentials destroys the prompt it
// was about to ask next.
func TestSessionSendsNothingElseWhenAnsweringCredentials(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Auth' username/password")
	})

	st := rec.await(t, "credential prompt", func(s Status) bool { return s.Prompt != nil })
	before := len(runner.server().commands())

	if err := session.Answer(Answer{
		PromptID: st.Prompt.ID, Username: "alice", Password: "s3cret",
	}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	awaitCommand(t, runner.server(), `password "Auth" "s3cret"`)

	// Give anything stray a chance to show up before asserting.
	time.Sleep(600 * time.Millisecond)

	sent := runner.server().commands()[before:]
	if len(sent) != 2 {
		t.Fatalf("answering sent %d commands, want exactly 2: %v", len(sent), sent)
	}
	for _, cmd := range sent {
		if !strings.HasPrefix(cmd, "username ") && !strings.HasPrefix(cmd, "password ") {
			t.Errorf("unexpected command %q while answering credentials", cmd)
		}
	}
}

// TestSessionReleasesHoldAfterRestart is the regression test for a connection
// that hung forever with no error.
//
// openvpn is started with --management-hold, so it holds at launch *and again
// after every soft restart*. A rejected passphrase restarts it, and if that
// second hold is never released the tunnel simply never retries: the UI shows
// "Reconnecting" indefinitely and openvpn logs nothing more.
func TestSessionReleasesHoldAfterRestart(t *testing.T) {
	_, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Private Key' password")
		f.notify(">PASSWORD:Verification Failed: 'Private Key'")
		f.notify(">STATE:1700000006,RECONNECTING,private-key-password-failure,,,,,")
		// openvpn asks again, with its own backoff hint.
		f.notify(">HOLD:Waiting for hold release:1")
	})

	rec.await(t, "reconnecting", func(s Status) bool { return s.Phase == PhaseReconnecting })

	// Two releases in total: the launch hold and the one after the restart.
	deadline := time.After(15 * time.Second)
	for {
		releases := 0
		for _, cmd := range runner.server().commands() {
			if cmd == "hold release" {
				releases++
			}
		}
		if releases >= 2 {
			return
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the hold after the restart was never released; commands: %v",
				runner.server().commands())
		}
	}
}

// TestSessionReleasesHoldOnce guards the other direction. Every command we send
// makes openvpn discard queued notifications, so releasing a hold twice can
// destroy the state transitions and prompts of the burst that follows.
func TestSessionReleasesHoldOnce(t *testing.T) {
	_, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
	})
	rec.await(t, "connected", func(s Status) bool { return s.Phase == PhaseConnected })

	// Well past the fallback that covers a missing hold notification.
	time.Sleep(holdReleaseFallback + time.Second)

	releases := 0
	for _, cmd := range runner.server().commands() {
		if cmd == "hold release" {
			releases++
		}
	}
	if releases != 1 {
		t.Errorf("sent %d hold releases for one hold, want 1: %v",
			releases, runner.server().commands())
	}
}

func TestHoldDelay(t *testing.T) {
	for hint, want := range map[string]time.Duration{
		"Waiting for hold release:0":    0,
		"Waiting for hold release:5":    5 * time.Second,
		"Waiting for hold release:1":    time.Second,
		"Waiting for hold release:9999": maxHoldDelay,
		"Waiting for hold release":      0,
		"":                              0,
		"Waiting for hold release:junk": 0,
	} {
		if got := holdDelay(hint); got != want {
			t.Errorf("holdDelay(%q) = %v, want %v", hint, got, want)
		}
	}
}

// startSessionWithSecrets is startSession with a stored-credential lookup, for
// the tests that care about not being asked.
func startSessionWithSecrets(
	t *testing.T,
	lookup SecretLookup,
	script func(*fakeOpenVPN),
) (*Session, *fakeRunner, *recorder) {
	t.Helper()
	runner := &fakeRunner{script: script, t: t}
	rec := newRecorder()
	handlers := rec.handlers()
	handlers.LookupSecret = lookup

	session := newSession(runner, SessionConfig{
		ProfileID:   "test",
		ProfileName: "Test Profile",
		ConfigPath:  t.TempDir() + "/test.ovpn",
		LogPath:     t.TempDir() + "/test.log",
	}, handlers)

	if err := session.start(context.Background(), 3); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { session.Disconnect() })
	return session, runner, rec
}

// TestSessionAnswersFromStorageWithoutAsking is the point of storing a secret:
// having saved it once, the user is not asked again.
func TestSessionAnswersFromStorageWithoutAsking(t *testing.T) {
	lookup := func(profileID string, kind PromptKind) (Secret, bool) {
		if kind == PromptPassphrase {
			return Secret{Password: "stored-passphrase"}, true
		}
		return Secret{Username: "alice", Password: "stored-password"}, true
	}

	_, runner, rec := startSessionWithSecrets(t, lookup, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Auth' username/password")
		f.notify(">PASSWORD:Need 'Private Key' password")
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
	})

	rec.await(t, "connected", func(s Status) bool { return s.Phase == PhaseConnected })

	awaitCommand(t, runner.server(), `username "Auth" "alice"`)
	awaitCommand(t, runner.server(), `password "Auth" "stored-password"`)
	awaitCommand(t, runner.server(), `password "Private Key" "stored-passphrase"`)

	// The user must never have seen a dialog.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, s := range rec.statuses {
		if s.Prompt != nil {
			t.Fatalf("a prompt was shown despite both secrets being stored: %+v", s.Prompt)
		}
	}
}

// TestSessionAsksAgainWhenStoredSecretIsRejected covers the other half. A saved
// passphrase can go stale, and repeating it forever would leave the user staring
// at a reconnect loop with no way to correct it.
func TestSessionAsksAgainWhenStoredSecretIsRejected(t *testing.T) {
	lookup := func(profileID string, kind PromptKind) (Secret, bool) {
		return Secret{Password: "stale-passphrase"}, true
	}

	_, runner, rec := startSessionWithSecrets(t, lookup, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Private Key' password")
		f.notify(">PASSWORD:Verification Failed: 'Private Key'")
		f.notify(">STATE:1700000006,RECONNECTING,private-key-password-failure,,,,,")
	})

	// The stored value is tried once...
	awaitCommand(t, runner.server(), `password "Private Key" "stale-passphrase"`)
	rec.await(t, "reconnecting", func(s Status) bool { return s.Phase == PhaseReconnecting })

	// ...and the restarted openvpn asks the same question again.
	runner.server().notify(">PASSWORD:Need 'Private Key' password")

	prompt := rec.await(t, "prompt after rejection", func(s Status) bool { return s.Prompt != nil })
	if prompt.Prompt.Kind != PromptPassphrase {
		t.Errorf("Kind = %q, want %q", prompt.Prompt.Kind, PromptPassphrase)
	}
	if !prompt.Prompt.Retry {
		t.Error("Retry = false; the dialog should say the previous attempt failed")
	}

	// And the stale value must not have been sent a second time.
	sent := 0
	for _, cmd := range runner.server().commands() {
		if strings.Contains(cmd, "stale-passphrase") {
			sent++
		}
	}
	if sent != 1 {
		t.Errorf("stored passphrase was sent %d times, want exactly 1", sent)
	}
}

// TestSessionDoesNotReuseStorageForChallenges guards one-time codes. A stored
// answer is worthless for a challenge and sending one would fail while looking
// like a bug.
func TestSessionDoesNotReuseStorageForChallenges(t *testing.T) {
	lookup := func(profileID string, kind PromptKind) (Secret, bool) {
		return Secret{Username: "alice", Password: "stored"}, true
	}

	_, _, rec := startSessionWithSecrets(t, lookup, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Auth' username/password SC:1,Enter your code")
	})

	st := rec.await(t, "static challenge prompt", func(s Status) bool { return s.Prompt != nil })
	if st.Prompt.Kind != PromptStaticChallenge {
		t.Errorf("Kind = %q, want %q", st.Prompt.Kind, PromptStaticChallenge)
	}
}

// TestSessionAsksWhenNothingStored is the plain case: no lookup, always ask.
func TestSessionAsksWhenNothingStored(t *testing.T) {
	lookup := func(profileID string, kind PromptKind) (Secret, bool) {
		return Secret{}, false
	}
	_, _, rec := startSessionWithSecrets(t, lookup, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Private Key' password")
	})
	st := rec.await(t, "prompt", func(s Status) bool { return s.Prompt != nil })
	if st.Prompt.Retry {
		t.Error("Retry = true on a first ask")
	}
}

func TestSessionHandlesStaticChallenge(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Auth' username/password SC:1,Enter your code")
	})

	st := rec.await(t, "static challenge", func(s Status) bool { return s.Prompt != nil })
	if st.Prompt.Kind != PromptStaticChallenge {
		t.Fatalf("Kind = %q, want %q", st.Prompt.Kind, PromptStaticChallenge)
	}
	if st.Prompt.ChallengeText != "Enter your code" {
		t.Errorf("ChallengeText = %q", st.Prompt.ChallengeText)
	}
	if !st.Prompt.EchoResponse {
		t.Error("EchoResponse = false, but the server said the code may be shown")
	}

	if err := session.Answer(Answer{
		PromptID: st.Prompt.ID, Username: "alice", Password: "pw", Response: "123456",
	}); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	// Password and second factor travel together, base64 encoded, in one value.
	// "pw" -> cHc=, "123456" -> MTIzNDU2
	awaitCommand(t, runner.server(), "SCRV1:cHc=:MTIzNDU2")
}

func TestSessionHandlesDynamicChallenge(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		// "YWxpY2U=" is "alice"; the state id must come back verbatim.
		f.notify(">PASSWORD:Verification Failed: 'Auth' ['CRV1:R,E:state-7:YWxpY2U=:Enter the code from your token']")
	})

	st := rec.await(t, "dynamic challenge", func(s Status) bool { return s.Prompt != nil })
	if st.Prompt.Kind != PromptDynamicChallenge {
		t.Fatalf("Kind = %q, want %q", st.Prompt.Kind, PromptDynamicChallenge)
	}
	if st.Prompt.Username != "alice" {
		t.Errorf("Username = %q, want alice decoded from the challenge", st.Prompt.Username)
	}
	if st.Prompt.ChallengeText != "Enter the code from your token" {
		t.Errorf("ChallengeText = %q", st.Prompt.ChallengeText)
	}
	if !st.Prompt.Retry {
		t.Error("Retry = false, want true for a challenge after a rejection")
	}

	if err := session.Answer(Answer{PromptID: st.Prompt.ID, Response: "999111"}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	awaitCommand(t, runner.server(), "CRV1::state-7::999111")
}

func TestSessionRejectsStalePromptAnswer(t *testing.T) {
	session, _, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Need 'Auth' username/password")
	})
	rec.await(t, "prompt", func(s Status) bool { return s.Prompt != nil })

	err := session.Answer(Answer{PromptID: "not-the-current-prompt", Username: "a", Password: "b"})
	if err == nil {
		t.Fatal("Answer accepted a stale prompt id, want an error")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %v, want it to say the request expired", err)
	}
}

func TestSessionReportsAuthRejection(t *testing.T) {
	_, _, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">PASSWORD:Verification Failed: 'Auth'")
	})
	got := rec.await(t, "rejection", func(s Status) bool { return s.Error != "" })
	if !strings.Contains(got.Error, "rejected") {
		t.Errorf("Error = %q, want it to say the credentials were rejected", got.Error)
	}
	if got.Prompt != nil {
		t.Error("a bare rejection should not leave a prompt up")
	}
}

func TestSessionTranslatesFatalErrors(t *testing.T) {
	_, _, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">FATAL:All TAP-Windows adapters on this system are currently in use.")
	})
	got := rec.await(t, "failure", func(s Status) bool { return s.Phase == PhaseFailed })
	if !strings.Contains(got.Detail, "No tunnel adapter is free") {
		t.Errorf("Detail = %q, want the translated explanation rather than openvpn's wording", got.Detail)
	}
}

func TestSessionReportsReconnectReason(t *testing.T) {
	_, _, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
		f.notify(">STATE:1700000006,RECONNECTING,ping-restart,,,,,")
	})
	got := rec.await(t, "reconnecting", func(s Status) bool { return s.Phase == PhaseReconnecting })
	if !strings.Contains(got.Detail, "Lost contact") {
		t.Errorf("Detail = %q, want a plain explanation of ping-restart", got.Detail)
	}
	if !got.ConnectedAt.IsZero() {
		t.Error("ConnectedAt should be cleared while reconnecting, or the UI shows a stale uptime")
	}
}

func TestSessionEndsWhenOpenvpnExits(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
	})
	rec.await(t, "connected", func(s Status) bool { return s.Phase == PhaseConnected })

	runner.server().close()

	select {
	case <-session.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("session did not finish after openvpn went away")
	}
	if got := rec.latest(); got.Phase != PhaseIdle {
		t.Errorf("final phase = %q, want %q", got.Phase, PhaseIdle)
	}
}

func TestSessionDisconnectSendsSigterm(t *testing.T) {
	session, runner, rec := startSession(t, func(f *fakeOpenVPN) {
		f.notify(">STATE:1700000005,CONNECTED,SUCCESS,10.8.0.6,203.0.113.9,1194,,")
	})
	rec.await(t, "connected", func(s Status) bool { return s.Phase == PhaseConnected })

	if err := session.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	awaitCommand(t, runner.server(), "signal SIGTERM")

	select {
	case <-session.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("session did not finish after Disconnect")
	}
}

func TestSessionFailsWhenLaunchFails(t *testing.T) {
	runner := &fakeRunner{t: t, startErr: fmt.Errorf("the OpenVPN Interactive Service is not running")}
	rec := newRecorder()
	session := newSession(runner, SessionConfig{
		ProfileID: "p", ProfileName: "P",
		ConfigPath: t.TempDir() + "/p.ovpn",
	}, rec.handlers())

	err := session.start(context.Background(), 3)
	if err == nil {
		t.Fatal("start succeeded despite the launcher failing")
	}
	got := rec.latest()
	if got.Phase != PhaseFailed {
		t.Errorf("Phase = %q, want %q", got.Phase, PhaseFailed)
	}
	if !strings.Contains(got.Error, "Interactive Service") {
		t.Errorf("Error = %q, want the launcher's explanation", got.Error)
	}
}

// --- helpers --------------------------------------------------------------

func awaitCommand(t *testing.T, f *fakeOpenVPN, sub string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		if f.sawCommandContaining(sub) {
			return
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("command containing %q was never sent; got %v", sub, f.commands())
		}
	}
}

func indexOf(items []string, want string) int {
	for i, s := range items {
		if s == want {
			return i
		}
	}
	return -1
}
