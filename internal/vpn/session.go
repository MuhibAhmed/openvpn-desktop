package vpn

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MuhibAhmed/openvpn-desktop/internal/mgmt"
)

// Phase is the connection state as the UI thinks about it. openvpn reports a
// dozen states; a person only cares about a handful.
type Phase string

const (
	PhaseIdle           Phase = "idle"
	PhaseStarting       Phase = "starting"
	PhaseConnecting     Phase = "connecting"
	PhaseAuthenticating Phase = "authenticating"
	PhaseConfiguring    Phase = "configuring"
	PhaseConnected      Phase = "connected"
	PhaseReconnecting   Phase = "reconnecting"
	PhaseDisconnecting  Phase = "disconnecting"
	PhaseFailed         Phase = "failed"
)

// Status is the whole connection state, as one value the UI can render.
type Status struct {
	Phase Phase `json:"phase"`
	// ProfileID and ProfileName identify what we are connected to.
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	// Detail is a sentence for a person, not an openvpn state name.
	Detail string `json:"detail"`
	// Since is when this phase began.
	Since time.Time `json:"since"`
	// ConnectedAt is when the tunnel came up, zero if it has not.
	ConnectedAt time.Time `json:"connectedAt"`

	LocalIP    string `json:"localIp"`
	RemoteIP   string `json:"remoteIp"`
	RemotePort string `json:"remotePort"`

	BytesIn  uint64 `json:"bytesIn"`
	BytesOut uint64 `json:"bytesOut"`

	// Prompt is set when openvpn is waiting on the user for credentials.
	Prompt *Prompt `json:"prompt,omitempty"`
	// Error explains a failure, in the words we would show a user.
	Error string `json:"error,omitempty"`
	// Stalled marks a connection that has stopped making progress without
	// failing. openvpn can end up waiting for an answer to a question that was
	// never delivered, which otherwise looks like an indefinite spinner.
	Stalled bool `json:"stalled"`
}

// Connected reports whether the tunnel is up.
func (s Status) Connected() bool { return s.Phase == PhaseConnected }

// PromptKind distinguishes the credential shapes openvpn can ask for.
type PromptKind string

const (
	// PromptCredentials is a username and password.
	PromptCredentials PromptKind = "credentials"
	// PromptPassphrase is a private key passphrase; no username.
	PromptPassphrase PromptKind = "passphrase"
	// PromptStaticChallenge wants username, password and a second factor at once.
	PromptStaticChallenge PromptKind = "static-challenge"
	// PromptDynamicChallenge wants only a second factor, after the password was
	// accepted but judged insufficient.
	PromptDynamicChallenge PromptKind = "dynamic-challenge"
)

// Prompt is a request for credentials, shaped for a dialog.
type Prompt struct {
	// ID must be echoed back with the answer, so a stale dialog cannot answer a
	// newer prompt.
	ID   string     `json:"id"`
	Kind PromptKind `json:"kind"`
	// Realm is openvpn's scope for the credential, e.g. "Auth".
	Realm string `json:"realm"`
	// Title and Message are for the dialog.
	Title   string `json:"title"`
	Message string `json:"message"`
	// Username is prefilled when the server told us who it thinks we are.
	Username string `json:"username"`
	// ChallengeText is the question to put next to the second-factor field.
	ChallengeText string `json:"challengeText,omitempty"`
	// EchoResponse reports whether the second factor may be shown as typed.
	EchoResponse bool `json:"echoResponse"`
	// Retry marks a prompt that follows a rejected attempt.
	Retry bool `json:"retry"`
}

// Answer is a reply to a Prompt.
type Answer struct {
	PromptID string `json:"promptId"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Response is the second factor, for challenge prompts.
	Response string `json:"response"`
	// Remember asks us to store the credentials for next time.
	Remember bool `json:"remember"`
}

// LogLine is one line from openvpn, tagged for display.
type LogLine struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Secret is a stored answer to a credential prompt.
type Secret struct {
	Username string
	Password string
}

// SecretLookup returns a previously saved answer for a prompt, if there is one.
//
// It is how a connection completes without asking: the old GUI this app replaces
// never prompts for a secret it has saved, and neither should we.
type SecretLookup func(profileID string, kind PromptKind) (Secret, bool)

// Handlers receives everything the UI needs to know. All are optional and are
// called from a single goroutine, in order.
type Handlers struct {
	OnStatus func(Status)
	OnLog    func(LogLine)
	// LookupSecret supplies saved credentials so the user is not asked for
	// something already stored.
	LookupSecret SecretLookup
}

// Session drives one openvpn process from launch to exit.
//
// One Session is one connection attempt, including any reconnects openvpn does
// on its own. It is not reusable: connect again and you get a new Session.
type Session struct {
	profileID   string
	profileName string
	configPath  string
	logPath     string

	runner   Runner
	handlers Handlers

	mu     sync.Mutex
	status Status
	client *mgmt.Client
	proc   Process

	// pendingRealm remembers which realm the outstanding prompt belongs to.
	pendingRealm string
	pendingKind  PromptKind
	// answered records realms that have had an answer sent, so a saved secret is
	// tried at most once per connection.
	answered         map[string]bool
	pendingChallenge string
	promptSeq        int

	holdMu       sync.Mutex
	holdReleases int

	logs   *ring
	closed chan struct{}
	once   sync.Once
}

// SessionConfig describes what to connect to.
type SessionConfig struct {
	ProfileID   string
	ProfileName string
	ConfigPath  string
	// LogPath is where openvpn writes its own log.
	LogPath string
	// Verbosity is openvpn's --verb.
	Verbosity int
}

// newSession prepares a session without starting anything.
func newSession(runner Runner, cfg SessionConfig, handlers Handlers) *Session {
	return &Session{
		profileID:   cfg.ProfileID,
		profileName: cfg.ProfileName,
		configPath:  cfg.ConfigPath,
		logPath:     cfg.LogPath,
		runner:      runner,
		handlers:    handlers,
		logs:        newRing(2000),
		closed:      make(chan struct{}),
		status: Status{
			Phase:       PhaseStarting,
			ProfileID:   cfg.ProfileID,
			ProfileName: cfg.ProfileName,
			Detail:      "Starting openvpn",
			Since:       time.Now(),
		},
	}
}

// Status returns the current state.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Logs returns the buffered log lines, oldest first.
func (s *Session) Logs() []LogLine { return s.logs.all() }

// Done is closed once the session has finished and will emit nothing more.
func (s *Session) Done() <-chan struct{} { return s.closed }

// start launches openvpn and begins driving it. It returns once the management
// interface is subscribed; everything after that is asynchronous.
func (s *Session) start(ctx context.Context, verbosity int) error {
	port, err := freeLoopbackPort()
	if err != nil {
		return fmt.Errorf("could not reserve a local port for openvpn: %w", err)
	}
	password, err := randomToken()
	if err != nil {
		return err
	}

	proc, err := s.runner.Start(ctx, StartRequest{
		ConfigPath:         s.configPath,
		ManagementHost:     "127.0.0.1",
		ManagementPort:     port,
		ManagementPassword: password,
		LogPath:            s.logPath,
		Verbosity:          verbosity,
	})
	if err != nil {
		s.fail(err.Error())
		return err
	}
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()

	client, err := s.dialManagement(ctx, port, password, proc)
	if err != nil {
		proc.Stop()
		proc.Close()
		s.fail(err.Error())
		return err
	}
	// Record the protocol itself, not just openvpn's log. When a connection
	// hangs it is because a message went missing, and the only way to tell
	// which one is to be able to see what actually crossed the socket.
	client.Trace = func(direction, line string) {
		// Log lines are already captured, and a byte count every second would
		// bury everything worth reading.
		if strings.HasPrefix(line, ">LOG:") || strings.HasPrefix(line, ">BYTECOUNT:") {
			return
		}
		s.appendLog(LogLine{Level: "D", Text: direction + " " + redactCredentials(line)})
	}

	s.mu.Lock()
	s.client = client
	s.mu.Unlock()

	// Subscribe before releasing the hold, and take the state history in the
	// same exchange: openvpn discards queued notifications whenever it reads a
	// command, so the history is the only reliable way to catch up.
	history, err := client.StateOn()
	if err != nil {
		s.teardown(fmt.Sprintf("openvpn would not accept our commands: %v", err))
		return err
	}
	for _, line := range history {
		s.applyStateLine(line)
	}
	if err := client.LogOn(); err != nil {
		s.teardown(fmt.Sprintf("openvpn would not accept our commands: %v", err))
		return err
	}
	if err := client.ByteCount(1); err != nil {
		s.teardown(fmt.Sprintf("openvpn would not accept our commands: %v", err))
		return err
	}

	s.setPhase(PhaseConnecting, "Contacting the server")

	// The hold is released in response to openvpn's ">HOLD:" notification
	// rather than here. openvpn holds again after every soft restart -- a wrong
	// passphrase, a failed authentication, a ping timeout -- and each of those
	// holds needs its own release or the connection sits there forever. Driving
	// it from the notification gives exactly one release per hold, which is also
	// what keeps us from sending a redundant one while openvpn is initialising.
	go s.run(proc, client)
	go s.watchForStall()
	go s.releaseInitialHoldIfMissed()
	return nil
}

// holdReleaseFallback is how long to wait for openvpn's first ">HOLD:" before
// releasing anyway.
//
// openvpn re-announces the hold as soon as a management client connects, so in
// practice this never fires. It exists because a connection that silently never
// starts is the worst failure this app can have.
const holdReleaseFallback = 3 * time.Second

// maxHoldDelay caps how long we will honour openvpn's suggested backoff, so a
// large restart delay cannot look like a hang.
const maxHoldDelay = 30 * time.Second

// releaseInitialHoldIfMissed covers the case where the first hold notification
// never arrives.
func (s *Session) releaseInitialHoldIfMissed() {
	select {
	case <-s.closed:
		return
	case <-time.After(holdReleaseFallback):
	}

	s.holdMu.Lock()
	missed := s.holdReleases == 0
	if missed {
		s.holdReleases++
	}
	s.holdMu.Unlock()

	if missed {
		s.appendLog(LogLine{Level: "D", Text: "no hold notification arrived; starting the tunnel anyway"})
		s.sendHoldRelease()
	}
}

// releaseHold answers a ">HOLD:" notification, waiting out the delay openvpn
// asked for first.
//
// Respecting openvpn's own hint is what stops a failing profile turning into a
// tight retry loop: the initial hold asks for no delay, whereas a restart after
// a rejected credential asks for a few seconds.
func (s *Session) releaseHold(hint string) {
	s.holdMu.Lock()
	s.holdReleases++
	s.holdMu.Unlock()

	if delay := holdDelay(hint); delay > 0 {
		select {
		case <-s.closed:
			return
		case <-time.After(delay):
		}
	}
	s.sendHoldRelease()
}

func (s *Session) sendHoldRelease() {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return
	}
	if err := client.HoldRelease(); err != nil {
		s.appendLog(LogLine{Level: "N", Text: "could not start the tunnel: " + err.Error()})
	}
}

// holdDelay reads the seconds openvpn suggested from
// "Waiting for hold release:5".
func holdDelay(hint string) time.Duration {
	idx := strings.LastIndexByte(hint, ':')
	if idx < 0 {
		return 0
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(hint[idx+1:]))
	if err != nil || seconds <= 0 {
		return 0
	}
	if d := time.Duration(seconds) * time.Second; d < maxHoldDelay {
		return d
	}
	return maxHoldDelay
}

// stallAfter is how long a connection may sit in one phase, with nothing asked
// of the user, before we say so.
//
// Generous on purpose: a slow server, a DNS timeout or a retry can legitimately
// take tens of seconds, and crying wolf during a normal connect is worse than
// staying quiet.
const stallAfter = 45 * time.Second

// watchForStall reports a connection that has stopped progressing.
//
// This is a safety net, not a mechanism. openvpn discards queued output when it
// reads a command, so a question it asked can be destroyed in flight; it then
// waits forever for an answer nobody saw. The cause is avoided elsewhere by not
// writing at those moments, but if it ever happens again the user should see an
// explanation rather than a spinner that never resolves.
func (s *Session) watchForStall() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}

		s.mu.Lock()
		st := s.status
		waiting := st.Prompt == nil &&
			!st.Stalled &&
			time.Since(st.Since) > stallAfter &&
			isPreConnected(st.Phase)
		if waiting {
			s.status.Stalled = true
			st = s.status
		}
		s.mu.Unlock()

		if waiting {
			s.emitStatus(st)
		}
	}
}

// isPreConnected reports whether a phase is one where openvpn should still be
// making progress on its own.
func isPreConnected(p Phase) bool {
	switch p {
	case PhaseStarting, PhaseConnecting, PhaseAuthenticating, PhaseConfiguring:
		return true
	default:
		return false
	}
}

// dialManagement waits for openvpn to bind its management port. openvpn needs a
// moment, and it may also die in that window, so watch for both.
func (s *Session) dialManagement(ctx context.Context, port int, password string, proc Process) (*mgmt.Client, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(20 * time.Second)

	for attempt := 0; ; attempt++ {
		select {
		case <-proc.Done():
			return nil, fmt.Errorf("openvpn exited before it was ready. %s", logHint(s.logPath))
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		client, err := mgmt.Dial(dialCtx, addr, password)
		cancel()
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("openvpn never opened its control channel. %s", logHint(s.logPath))
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// run consumes management events until openvpn goes away.
func (s *Session) run(proc Process, client *mgmt.Client) {
	defer func() {
		client.Close()
		proc.Close()
		s.once.Do(func() { close(s.closed) })
	}()

	events := client.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				s.finish(client.Err())
				return
			}
			s.handle(ev)
		case <-proc.Done():
			// Drain whatever is left so a final FATAL is not lost.
			s.drain(events)
			s.finish(nil)
			return
		}
	}
}

// drain consumes any remaining buffered events without blocking for long.
func (s *Session) drain(events <-chan mgmt.Event) {
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			s.handle(ev)
		case <-timeout:
			return
		}
	}
}

func (s *Session) handle(ev mgmt.Event) {
	switch ev.Kind {
	case mgmt.KindState:
		s.applyState(ev)

	case mgmt.KindByteCount:
		s.mu.Lock()
		s.status.BytesIn = ev.BytesIn
		s.status.BytesOut = ev.BytesOut
		next := s.status
		s.mu.Unlock()
		s.emitStatus(next)

	case mgmt.KindLog:
		s.appendLog(LogLine{At: ev.Time, Level: ev.Level, Text: ev.Text})

	case mgmt.KindHold:
		// openvpn is waiting for permission to start, which it does at launch
		// and again after every soft restart.
		go s.releaseHold(ev.Text)

	case mgmt.KindPassword:
		s.applyPasswordRequest(ev.Password)

	case mgmt.KindFatal:
		s.fail(humaniseFatal(ev.Text))

	case mgmt.KindInfo, mgmt.KindEcho:
		s.appendLog(LogLine{At: ev.Time, Level: "I", Text: ev.Text})

	case mgmt.KindNeedOK:
		// Nothing here needs a decision from the user yet; confirm so openvpn
		// is not left waiting forever.
		s.mu.Lock()
		client := s.client
		s.mu.Unlock()
		if client != nil {
			client.NeedOK(firstQuoted(ev.Text), true)
		}
	}
}

// applyStateLine handles a history line from "state on all", which has the same
// comma-separated shape as a notification but no ">STATE:" prefix.
func (s *Session) applyStateLine(line string) {
	ev := mgmt.ParseStateLine(line)
	if ev.State == "" {
		return
	}
	s.applyState(ev)
}

func (s *Session) applyState(ev mgmt.Event) {
	phase, detail := describeState(ev)

	s.mu.Lock()
	if ev.LocalIP != "" {
		s.status.LocalIP = ev.LocalIP
	}
	if ev.RemoteIP != "" {
		s.status.RemoteIP = ev.RemoteIP
		s.status.RemotePort = ev.RemotePort
	}
	// A state transition means whatever we were prompting for is resolved.
	if phase != PhaseAuthenticating {
		s.status.Prompt = nil
	}
	if phase == PhaseConnected && s.status.ConnectedAt.IsZero() {
		s.status.ConnectedAt = time.Now()
	}
	if phase == PhaseReconnecting {
		// Counters restart with the new tunnel; keeping the old totals on
		// screen would be a lie.
		s.status.ConnectedAt = time.Time{}
	}
	changed := s.status.Phase != phase
	s.status.Phase = phase
	s.status.Detail = detail
	// Any transition is progress, so an earlier stall warning no longer stands.
	s.status.Stalled = false
	if changed {
		s.status.Since = time.Now()
	}
	next := s.status
	s.mu.Unlock()

	s.emitStatus(next)
}

// describeState maps an openvpn state onto a phase and a sentence a person can
// act on. openvpn's own vocabulary ("GET_CONFIG", "ASSIGN_IP") means nothing to
// the people this app is for.
func describeState(ev mgmt.Event) (Phase, string) {
	switch ev.State {
	case mgmt.StateConnecting:
		return PhaseConnecting, "Contacting the server"
	case mgmt.StateResolve:
		return PhaseConnecting, "Looking up the server address"
	case mgmt.StateTCPConnect:
		return PhaseConnecting, "Opening a connection to the server"
	case mgmt.StateWait:
		return PhaseConnecting, "Waiting for the server to respond"
	case mgmt.StateAuth:
		return PhaseAuthenticating, "Signing in"
	case mgmt.StateGetConfig:
		return PhaseConfiguring, "Getting settings from the server"
	case mgmt.StateAssignIP:
		return PhaseConfiguring, "Setting up the network adapter"
	case mgmt.StateAddRoutes:
		return PhaseConfiguring, "Applying network routes"
	case mgmt.StateConnected:
		return PhaseConnected, "Connected"
	case mgmt.StateReconnecting:
		return PhaseReconnecting, reconnectReason(ev.Detail)
	case mgmt.StateExiting:
		return PhaseDisconnecting, "Disconnecting"
	default:
		if ev.State == "" {
			return PhaseConnecting, "Connecting"
		}
		return PhaseConnecting, strings.ToLower(string(ev.State))
	}
}

// reconnectReason translates openvpn's terse reconnect causes.
func reconnectReason(detail string) string {
	switch detail {
	case "ping-restart", "ping-exit":
		return "Lost contact with the server, reconnecting"
	case "tls-error":
		return "Secure connection failed, reconnecting"
	case "auth-failure":
		return "The server rejected the credentials, reconnecting"
	case "connection-reset":
		return "The server closed the connection, reconnecting"
	case "init_instance":
		return "Reconnecting"
	case "":
		return "Reconnecting"
	default:
		return "Reconnecting after " + strings.ReplaceAll(detail, "-", " ")
	}
}

// humaniseFatal rewrites openvpn's fatal messages into something that tells the
// user what to do. Anything unrecognised is passed through: a raw message beats
// a wrong guess.
func humaniseFatal(text string) string {
	lower := strings.ToLower(text)
	switch {
	// openvpn words this as "All tap-windows6 adapters on this system are
	// currently in use or disabled", and the driver name varies by version, so
	// match on the part that does not.
	case strings.Contains(lower, "adapters on this system are currently in use"),
		strings.Contains(lower, "adapters are currently in use"):
		return "No tunnel adapter is free. Another VPN connection is probably still " +
			"running -- disconnect it first. A leftover openvpn process can also hold " +
			"the adapter after a crash."
	case strings.Contains(lower, "cannot load certificate"), strings.Contains(lower, "cannot load private key"):
		return "The certificate or key in this profile could not be read. The profile may be incomplete."
	case strings.Contains(lower, "private key password verification failed"):
		return "That passphrase was not correct for the private key in this profile."
	case strings.Contains(lower, "auth-failure"), strings.Contains(lower, "authenticate/decrypt"):
		return "The server rejected the credentials."
	case strings.Contains(lower, "no such file or directory"):
		return "A file this profile refers to is missing. Try importing it again."
	default:
		return text
	}
}

// applyPasswordRequest turns openvpn's credential query into a Prompt and
// publishes it. It does not answer anything itself: the UI decides.
func (s *Session) applyPasswordRequest(req mgmt.PasswordRequest) {
	// A bare "Verification Failed" with no challenge is a rejection, not a
	// prompt; openvpn will ask again separately if --auth-retry allows it.
	if req.Failed && req.DynamicChallenge == "" {
		detail := "The server rejected the credentials."
		// A private key is decrypted locally, so a failure there is a wrong
		// passphrase, not the server refusing anything.
		if strings.EqualFold(req.Realm, "Private Key") {
			detail = "That passphrase did not unlock this profile's private key."
		}
		if req.Reason != "" {
			detail = req.Reason
		}
		s.mu.Lock()
		s.status.Prompt = nil
		s.status.Error = detail
		next := s.status
		s.mu.Unlock()
		s.emitStatus(next)
		return
	}

	s.mu.Lock()
	s.promptSeq++
	prompt := &Prompt{
		ID:    fmt.Sprintf("%s-%d", s.profileID, s.promptSeq),
		Realm: req.Realm,
	}

	switch {
	case req.DynamicChallenge != "":
		prompt.Kind = PromptDynamicChallenge
		prompt.Title = "One more step"
		prompt.Message = "The server needs another code to finish signing in."
		prompt.ChallengeText = req.DynamicChallenge
		prompt.Username = req.ChallengeUser
		prompt.Retry = true
		s.pendingChallenge = req.ChallengeStateID

	case req.StaticChallenge != "":
		prompt.Kind = PromptStaticChallenge
		prompt.Title = "Sign in"
		prompt.Message = "This connection needs your credentials and a verification code."
		prompt.ChallengeText = req.StaticChallenge
		prompt.EchoResponse = req.StaticChallengeEcho

	case req.NeedUsername:
		prompt.Kind = PromptCredentials
		prompt.Title = "Sign in"
		// Phrased as what the server asked for, because whether a password is
		// actually required is the server's business, not something we can know.
		prompt.Message = "The server asked for a username and password."

	default:
		prompt.Kind = PromptPassphrase
		prompt.Title = "Unlock the certificate"
		prompt.Message = "This profile's private key is protected by a passphrase."
	}

	s.pendingRealm = req.Realm
	s.pendingKind = prompt.Kind

	// Only the first ask for a given secret may be answered from storage. If
	// openvpn asks a second time, what we had was rejected, and repeating it
	// would loop forever without ever telling the user why.
	alreadyTried := s.answered[req.Realm]
	if alreadyTried {
		prompt.Retry = true
	}
	client := s.client
	s.mu.Unlock()

	if !alreadyTried {
		if secret, ok := s.storedAnswerFor(prompt.Kind, req.Realm); ok {
			s.markAnswered(req.Realm)
			if err := s.sendStoredAnswer(client, req, secret); err == nil {
				// Deliberately no prompt: the whole point is that a saved
				// secret is not asked for again.
				s.setPhase(PhaseAuthenticating, "Signing in")
				return
			}
			// Falling through shows the dialog, which is the right outcome for
			// a write that failed.
		}
	}

	s.mu.Lock()
	s.status.Prompt = prompt
	s.status.Phase = PhaseAuthenticating
	s.status.Detail = "Waiting for credentials"
	s.status.Stalled = false
	s.status.Since = time.Now()
	next := s.status
	s.mu.Unlock()

	s.emitStatus(next)
}

// storedAnswerFor returns a saved answer, for the prompt kinds where reusing one
// makes sense.
//
// Challenges are excluded on purpose: a one-time code is worthless the second
// time, so re-sending a stored one would just fail while looking like a bug.
func (s *Session) storedAnswerFor(kind PromptKind, realm string) (Secret, bool) {
	if s.handlers.LookupSecret == nil {
		return Secret{}, false
	}
	switch kind {
	case PromptCredentials, PromptPassphrase:
	default:
		return Secret{}, false
	}
	return s.handlers.LookupSecret(s.profileID, kind)
}

// sendStoredAnswer replies to openvpn without involving the user.
func (s *Session) sendStoredAnswer(client *mgmt.Client, req mgmt.PasswordRequest, secret Secret) error {
	if client == nil {
		return fmt.Errorf("no management connection")
	}
	s.appendLog(LogLine{Level: "D", Text: "answering " + req.Realm + " from saved credentials"})
	if req.NeedUsername {
		return client.Credentials(req.Realm, secret.Username, secret.Password)
	}
	return client.Password(req.Realm, secret.Password)
}

// markAnswered records that a realm has had its one automatic attempt. Any
// answer counts, including one the user typed, so a stored value can never
// override what they just entered.
func (s *Session) markAnswered(realm string) {
	s.mu.Lock()
	if s.answered == nil {
		s.answered = make(map[string]bool)
	}
	s.answered[realm] = true
	s.mu.Unlock()
}

// Answer supplies credentials for the outstanding prompt.
func (s *Session) Answer(a Answer) error {
	s.mu.Lock()
	client := s.client
	prompt := s.status.Prompt
	realm := s.pendingRealm
	challenge := s.pendingChallenge
	s.mu.Unlock()

	if client == nil {
		return fmt.Errorf("this connection is no longer running")
	}
	if prompt == nil {
		return fmt.Errorf("nothing is waiting for credentials")
	}
	if a.PromptID != "" && a.PromptID != prompt.ID {
		// A dialog answering a prompt that has already been superseded.
		return fmt.Errorf("this request has expired, please try again")
	}

	var err error
	switch prompt.Kind {
	case PromptPassphrase:
		err = client.Password(realm, a.Password)
	case PromptStaticChallenge:
		err = client.AnswerStaticChallenge(realm, a.Username, a.Password, a.Response)
	case PromptDynamicChallenge:
		username := a.Username
		if username == "" {
			username = prompt.Username
		}
		err = client.AnswerDynamicChallenge(realm, username, challenge, a.Response)
	default:
		err = client.Credentials(realm, a.Username, a.Password)
	}
	if err != nil {
		return err
	}
	// Count this realm as answered, so if openvpn asks again it goes back to the
	// user rather than being auto-answered with a stored value they have just
	// overridden.
	s.markAnswered(realm)

	// Nothing else may be sent here. openvpn discards whatever it has queued
	// but not yet flushed as soon as input arrives, and the very next thing it
	// usually queues is the next question -- the private key passphrase. An
	// extra command at this point destroys that prompt, and the connection then
	// hangs forever waiting for an answer to a question nobody saw.
	s.mu.Lock()
	s.status.Prompt = nil
	s.status.Error = ""
	s.status.Detail = "Signing in"
	s.status.Stalled = false
	s.status.Since = time.Now()
	next := s.status
	s.mu.Unlock()
	s.emitStatus(next)

	return nil
}

// Disconnect asks openvpn to shut down, preferring the management interface and
// falling back to the exit event if that is not answering.
func (s *Session) Disconnect() error {
	s.mu.Lock()
	client := s.client
	proc := s.proc
	if s.status.Phase != PhaseFailed && s.status.Phase != PhaseIdle {
		s.status.Phase = PhaseDisconnecting
		s.status.Detail = "Disconnecting"
		s.status.Prompt = nil
		s.status.Since = time.Now()
	}
	next := s.status
	s.mu.Unlock()
	s.emitStatus(next)

	if client != nil {
		if err := client.Disconnect(); err == nil {
			return nil
		}
	}
	if proc != nil {
		return proc.Stop()
	}
	return nil
}

// finish records the end of the session.
func (s *Session) finish(err error) {
	s.mu.Lock()
	if s.status.Phase != PhaseFailed {
		s.status.Phase = PhaseIdle
		s.status.Detail = "Not connected"
		s.status.Prompt = nil
		s.status.ConnectedAt = time.Time{}
		s.status.Since = time.Now()
	}
	next := s.status
	s.mu.Unlock()
	s.emitStatus(next)
}

// fail moves the session into a terminal error state.
func (s *Session) fail(message string) {
	s.mu.Lock()
	s.status.Phase = PhaseFailed
	s.status.Detail = message
	s.status.Error = message
	s.status.Prompt = nil
	s.status.Since = time.Now()
	next := s.status
	s.mu.Unlock()
	s.emitStatus(next)
}

// teardown fails the session and stops openvpn.
func (s *Session) teardown(message string) {
	s.fail(message)
	s.mu.Lock()
	client, proc := s.client, s.proc
	s.mu.Unlock()
	if client != nil {
		client.Close()
	}
	if proc != nil {
		proc.Stop()
		proc.Close()
	}
	s.once.Do(func() { close(s.closed) })
}

func (s *Session) setPhase(phase Phase, detail string) {
	s.mu.Lock()
	s.status.Phase = phase
	s.status.Detail = detail
	s.status.Since = time.Now()
	next := s.status
	s.mu.Unlock()
	s.emitStatus(next)
}

func (s *Session) emitStatus(st Status) {
	if s.handlers.OnStatus != nil {
		s.handlers.OnStatus(st)
	}
}

func (s *Session) appendLog(line LogLine) {
	if line.At.IsZero() {
		line.At = time.Now()
	}
	s.logs.add(line)
	if s.handlers.OnLog != nil {
		s.handlers.OnLog(line)
	}
}

// --- helpers --------------------------------------------------------------

// freeLoopbackPort asks the OS for an unused port. There is an unavoidable race
// between releasing it and openvpn binding it, so callers must treat a bind
// failure as retryable rather than fatal.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// redactCredentials strips secrets out of a management line before it reaches
// the log. The whole point of the log is that people can paste it when asking
// for help, which must never mean pasting their password.
//
// The management interface's own password is not a concern here: it is sent
// during the handshake, before the trace hook is attached.
func redactCredentials(line string) string {
	for _, prefix := range []string{"password ", "username ", "cr-response ", "needstr "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(prefix) + " [redacted]"
		}
	}
	return line
}

func logHint(path string) string {
	if path == "" {
		return "There is no log to check."
	}
	return "See " + path + " for what openvpn reported."
}

func firstQuoted(s string) string {
	open := strings.Index(s, "'")
	if open < 0 {
		return s
	}
	rest := s[open+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		return s
	}
	return rest[:end]
}

// ring is a fixed-size log buffer. Connection logs are unbounded in principle
// and nobody scrolls back further than this.
type ring struct {
	mu    sync.Mutex
	items []LogLine
	limit int
}

func newRing(limit int) *ring { return &ring{limit: limit} }

func (r *ring) add(line LogLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, line)
	if len(r.items) > r.limit {
		r.items = append([]LogLine(nil), r.items[len(r.items)-r.limit:]...)
	}
}

func (r *ring) all() []LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LogLine(nil), r.items...)
}
