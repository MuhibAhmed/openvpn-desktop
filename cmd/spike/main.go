//go:build windows

// Command spike validates the Windows connection path end to end, without any
// UI in the way. It is the M1 gate: if this works from an unelevated shell, the
// architecture holds.
//
// It exercises three things that are hard to be sure about from documentation
// alone:
//
//  1. the Interactive Service pipe protocol (message framing, reply format),
//  2. the config-directory rule the service enforces on non-admin callers,
//  3. the management-interface handshake, notifications, and clean shutdown.
//
// Usage:
//
//	go run ./cmd/spike -config <path to .ovpn>   # existing profile
//	go run ./cmd/spike -selftest                 # writes an unroutable config
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MuhibAhmed/openvpn-desktop/internal/mgmt"
	"github.com/MuhibAhmed/openvpn-desktop/internal/ovpn"
	"github.com/MuhibAhmed/openvpn-desktop/internal/svcpipe"
)

// selftestConfig is a static-key point-to-point tunnel aimed at TEST-NET-2,
// which is guaranteed unroutable.
//
// Static-key mode is deliberate: it needs no PKI, and openvpn brings the tunnel
// interface up immediately rather than waiting for a TLS handshake. That means
// this config exercises the privileged path too -- adapter open and ifconfig
// are both delegated to the Interactive Service over the message channel -- so
// reaching CONNECTED here proves the whole launch chain without a server.
const selftestConfig = `dev tun
proto udp
remote 198.51.100.1 1194
nobind
persist-key
persist-tun
ifconfig 10.99.0.2 10.99.0.1
secret static.key
verb 4
`

// loopbackClientConfig connects to a peer we run ourselves on localhost, so the
// tunnel actually comes up and openvpn reports CONNECTED. Without a peer,
// openvpn opens the adapter and starts sending but never completes its
// initialisation sequence, so the most important state for the UI is never
// exercised.
const loopbackClientConfig = `dev tun
proto udp
remote 127.0.0.1 %d
nobind
persist-key
persist-tun
ifconfig 10.99.0.2 10.99.0.1
secret static.key
ping 1
verb 3
`

// tlsClientConfig connects to a local TLS peer using a certificate whose
// private key is encrypted, so openvpn asks for the passphrase over the
// management interface.
//
// That prompt is the one a real connection hangs on if anything writes to the
// management socket while it is queued, so this config is how that path gets
// tested without a server or anyone's credentials.
const tlsClientConfig = `dev tun
proto udp
remote 127.0.0.1 %d
tls-client
nobind
persist-key
persist-tun
ca ca.crt
cert client.crt
key client.key
dh none
ifconfig 10.98.0.2 10.98.0.1
ping 1
verb 3
`

// tlsServerArgs is the matching peer. "dev null" means it needs no adapter and
// no privileges.
func tlsServerArgs(dir string, port int) []string {
	return []string{
		"--dev", "null",
		"--proto", "udp",
		"--lport", fmt.Sprint(port),
		"--tls-server",
		"--ca", filepath.Join(dir, "ca.crt"),
		"--cert", filepath.Join(dir, "server.crt"),
		"--key", filepath.Join(dir, "server.key"),
		"--dh", "none",
		"--ifconfig", "10.98.0.1", "10.98.0.2",
		"--ping", "1",
		"--verb", "3",
	}
}

func main() {
	var (
		configPath = flag.String("config", "", "path to an .ovpn file (must be inside a permitted config dir)")
		selftest   = flag.Bool("selftest", false, "write a throwaway config with an unroutable remote and connect to it")
		loopback   = flag.Bool("loopback", false, "run a local peer so the tunnel reaches CONNECTED, then connect to it")
		tlsMode    = flag.Bool("tls", false, "like -loopback but over TLS with an encrypted private key, so the passphrase prompt is exercised")
		keypass    = flag.String("keypass", "", "passphrase to encrypt the generated key with, for -tls (default: a fixed test value)")
		configDir  = flag.String("configdir", "", "directory to write the selftest config into (default: %USERPROFILE%\\OpenVPN\\config\\spike)")
		username   = flag.String("user", "", "username to answer an Auth prompt with (otherwise prompted on the terminal)")
		password   = flag.String("pass", "", "password to answer an Auth prompt with")
		debug      = flag.Bool("debug", false, "echo every management line verbatim")
		extra      = flag.String("extra", "", "extra openvpn options to append (used to probe what the service permits)")
		timeout    = flag.Duration("timeout", 60*time.Second, "how long to observe before disconnecting")
	)
	flag.Parse()
	debugRaw = *debug

	extraOptions = *extra
	// A TLS run generates its key, so it knows the passphrase and can answer
	// its own prompt.
	if *tlsMode {
		if *keypass != "" {
			keyPassphrase = *keypass
		}
		if *password == "" {
			*password = keyPassphrase
		}
	}
	if err := run(*configPath, *selftest || *loopback || *tlsMode, *loopback, *tlsMode, *configDir, *username, *password, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(configPath string, selftest, loopback, tlsMode bool, configDir, username, password string, timeout time.Duration) error {
	switch {
	case selftest:
		dir := configDir
		if dir == "" {
			dir = filepath.Join(os.Getenv("USERPROFILE"), "OpenVPN", "config", "spike")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
		configPath = filepath.Join(dir, "spike.ovpn")

		install, err := ovpn.Detect()
		if err != nil {
			return err
		}
		version, err := install.Version()
		if err != nil {
			return err
		}
		logf("detected %s", version)
		logf("  exe        %s", install.ExePath)
		logf("  config dir %s", install.ConfigDir)
		logf("  wintun     %v", install.HasWintun())

		keyPath := filepath.Join(dir, "static.key")
		if _, err := os.Stat(keyPath); err != nil {
			if err := install.GenKey(keyPath); err != nil {
				return err
			}
			logf("generated static key at %s", keyPath)
		}

		body := selftestConfig
		if tlsMode {
			logf("generating a throwaway PKI with an encrypted client key")
			if _, err := generatePKI(dir); err != nil {
				return err
			}
			peerPort, err := freeUDPPort()
			if err != nil {
				return err
			}
			peer := exec.Command(install.ExePath, tlsServerArgs(dir, peerPort)...)
			if err := peer.Start(); err != nil {
				return fmt.Errorf("start TLS peer: %w", err)
			}
			defer func() {
				peer.Process.Kill()
				peer.Wait()
			}()
			logf("TLS peer running on udp/%d (pid %d)", peerPort, peer.Process.Pid)
			body = fmt.Sprintf(tlsClientConfig, peerPort)
		} else if loopback {
			peerPort, err := freeUDPPort()
			if err != nil {
				return err
			}
			// The peer uses "dev null", so it needs no tunnel adapter and no
			// privileges -- we can just run it directly as ourselves.
			peer := exec.Command(install.ExePath,
				"--dev", "null",
				"--proto", "udp",
				"--lport", strconv.Itoa(peerPort),
				"--ifconfig", "10.99.0.1", "10.99.0.2",
				"--secret", keyPath,
				"--ping", "1",
				"--verb", "3",
			)
			if err := peer.Start(); err != nil {
				return fmt.Errorf("start loopback peer: %w", err)
			}
			defer func() {
				peer.Process.Kill()
				peer.Wait()
			}()
			logf("loopback peer running on udp/%d (pid %d)", peerPort, peer.Process.Pid)
			body = fmt.Sprintf(loopbackClientConfig, peerPort)
		}
		if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
			return fmt.Errorf("write selftest config: %w", err)
		}
		logf("wrote config to %s", configPath)
	case configPath == "":
		return fmt.Errorf("pass -config <file.ovpn> or -selftest")
	}

	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config file: %w", err)
	}

	port, err := freePort()
	if err != nil {
		return fmt.Errorf("find a free port: %w", err)
	}
	mgmtPassword, err := randomToken()
	if err != nil {
		return err
	}

	// --service puts openvpn in a mode where it reads the management password
	// from stdin instead of a console. The interactive service starts it without
	// a console, so this is mandatory: omit it and openvpn exits 1 immediately.
	eventName := "openvpn-desktop-exit-" + mgmtPassword[:8]
	exitEvent, err := svcpipe.NewExitEvent(eventName)
	if err != nil {
		return err
	}
	defer exitEvent.Close()

	// A log file is the only way to see why openvpn died before the management
	// interface came up -- the service does not forward its stdout.
	logPath := filepath.Join(filepath.Dir(configPath), "spike.log")

	// The service validates our command line against a short whitelist
	// (openvpnserv/validate.c white_list[]). Anything not in that list -- every
	// networking option included -- has to live inside the .ovpn file instead.
	workDir := filepath.Dir(configPath)
	options := strings.Join([]string{
		`--config "` + filepath.Base(configPath) + `"`,
		fmt.Sprintf("--management 127.0.0.1 %d stdin", port),
		"--management-hold",
		"--management-query-passwords",
		"--auth-retry interact",
		"--service " + exitEvent.Name() + " 0",
		`--log "` + logPath + `"`,
		"--verb 4",
		extraOptions,
	}, " ")

	logf("workdir: %s", workDir)
	logf("options: %s", options)
	logf("openvpn log: %s", logPath)

	logf("step 1/4: connecting to %s", svcpipe.PipeName)
	pipe, err := svcpipe.Dial()
	if err != nil {
		return fmt.Errorf("interactive service unreachable (is OpenVPNServiceInteractive running?): %w", err)
	}
	defer pipe.Close()

	logf("step 2/4: sending startup data")
	if err := pipe.Send(svcpipe.Startup{
		WorkingDir: workDir,
		Options:    options,
		// The management password goes over openvpn's stdin so it never
		// touches disk.
		Stdin: mgmtPassword + "\n",
	}); err != nil {
		return err
	}

	reply, err := pipe.Recv()
	if err != nil {
		return fmt.Errorf("read service reply: %w", err)
	}
	if !reply.OK() {
		return fmt.Errorf("service refused to start openvpn: %s", reply.Error())
	}
	logf("openvpn started, pid %d", reply.PID)

	// The service disconnects the pipe when openvpn exits. Watch for that.
	processGone := make(chan struct{})
	go func() {
		defer close(processGone)
		for {
			r, err := pipe.Recv()
			if err != nil {
				return
			}
			logf("service message: %s", strings.ReplaceAll(strings.TrimSpace(r.Raw), "\n", " | "))
		}
	}()

	logf("step 3/4: connecting to management interface on 127.0.0.1:%d", port)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// openvpn needs a moment to bind the management port.
	var client *mgmt.Client
	for attempt := 1; ; attempt++ {
		client, err = mgmt.Dial(ctx, fmt.Sprintf("127.0.0.1:%d", port), mgmtPassword)
		if err == nil {
			break
		}
		if attempt >= 30 {
			dumpLog(logPath)
			return fmt.Errorf("management interface never came up: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	defer client.Close()
	if debugRaw {
		client.Trace = func(direction, line string) {
			fmt.Printf("  %s %s\n", direction, line)
		}
	}
	logf("management interface authenticated")

	logf("step 4/4: subscribing and releasing the hold")

	// Each of these waits for its own acknowledgement before the next is sent.
	// Pipelining them loses both commands and notifications.
	stateHistory, err := client.StateOn()
	if err != nil {
		return fmt.Errorf("state on: %w", err)
	}
	for _, h := range stateHistory {
		fmt.Printf("  history    %s\n", h)
	}
	if err := client.LogOn(); err != nil {
		return fmt.Errorf("log on: %w", err)
	}
	if err := client.ByteCount(1); err != nil {
		return fmt.Errorf("bytecount: %w", err)
	}
	if err := client.HoldRelease(); err != nil {
		return fmt.Errorf("hold release: %w", err)
	}

	return observe(client, processGone, username, password, timeout)
}

// observe prints everything openvpn tells us until the deadline, the process
// dies, or the user interrupts -- then disconnects cleanly and reports whether
// the teardown was honoured.
func observe(client *mgmt.Client, processGone <-chan struct{}, username, password string, timeout time.Duration) error {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	deadline := time.After(timeout)
	fmt.Println()

	var sawConnected, sawBytecount bool
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				return fmt.Errorf("management connection closed: %v", client.Err())
			}
			report(client, ev, username, password, &sawConnected, &sawBytecount)

		case <-processGone:
			return fmt.Errorf("openvpn exited (service disconnected the pipe)")

		case <-interrupt:
			logf("interrupted; sending SIGTERM")
			return teardown(client, sawConnected, sawBytecount)

		case <-deadline:
			logf("observation window elapsed; sending SIGTERM")
			return teardown(client, sawConnected, sawBytecount)
		}
	}
}

// debugRaw echoes every line exactly as openvpn sent it, which is the only way
// to tell a parsing bug from a message we never received.
var debugRaw bool

// extraOptions is appended to the openvpn command line verbatim, to probe
// which options the Interactive Service will accept from this caller.
var extraOptions string

func report(client *mgmt.Client, ev mgmt.Event, username, password string, sawConnected, sawBytecount *bool) {
	if debugRaw {
		fmt.Printf("  raw[%s] %q\n", ev.Kind, ev.Raw)
	}
	switch ev.Kind {
	case mgmt.KindState:
		fmt.Printf("  STATE      %-14s %s", ev.State, ev.Detail)
		if ev.LocalIP != "" {
			fmt.Printf("  local=%s", ev.LocalIP)
		}
		if ev.RemoteIP != "" {
			fmt.Printf("  remote=%s:%s", ev.RemoteIP, ev.RemotePort)
		}
		fmt.Println()
		if ev.State == mgmt.StateConnected {
			*sawConnected = true
		}

	case mgmt.KindByteCount:
		*sawBytecount = true
		fmt.Printf("  BYTES      in=%d out=%d\n", ev.BytesIn, ev.BytesOut)

	case mgmt.KindLog:
		fmt.Printf("  log  [%s]  %s\n", ev.Level, ev.Text)

	case mgmt.KindHold:
		fmt.Printf("  HOLD       %s\n", ev.Text)
		// The first hold is the one already released explicitly during startup.
		// Any later hold is openvpn asking permission to retry after a soft
		// restart, and without a release it waits there forever -- which is
		// exactly how a rejected passphrase turned into an endless spinner.
		holdsSeen++
		if holdsSeen > 1 {
			go func(hint string) {
				if d := holdDelay(hint); d > 0 {
					fmt.Printf("  HOLD       waiting %s before retrying\n", d)
					time.Sleep(d)
				}
				if err := client.HoldRelease(); err != nil {
					fmt.Printf("  ! hold release failed: %v\n", err)
				}
			}(ev.Text)
		}

	case mgmt.KindPassword:
		answerPassword(client, ev.Password, username, password)

	case mgmt.KindNeedOK:
		fmt.Printf("  NEED-OK    %s (answering ok)\n", ev.Text)
		// The realm is the first quoted token; reuse the parser's convention.
		client.NeedOK(firstQuoted(ev.Text), true)

	case mgmt.KindFatal:
		fmt.Printf("  FATAL      %s\n", ev.Text)

	case mgmt.KindInfo:
		fmt.Printf("  INFO       %s\n", ev.Text)

	case mgmt.KindReply:
		if strings.HasPrefix(ev.Text, "ERROR:") {
			fmt.Printf("  ! %s\n", ev.Text)
		}
	}
}

// answerPassword handles the three credential shapes. The spike prompts on the
// terminal; the app will drive this from a modal instead.
func answerPassword(client *mgmt.Client, req mgmt.PasswordRequest, username, password string) {
	switch {
	case req.Failed && req.DynamicChallenge != "":
		fmt.Printf("  2FA        dynamic challenge for %q: %s\n", req.Realm, req.DynamicChallenge)
		user := req.ChallengeUser
		if user == "" {
			user = username
		}
		answer := ask("  response: ")
		if err := client.AnswerDynamicChallenge(req.Realm, user, req.ChallengeStateID, answer); err != nil {
			fmt.Printf("  ! %v\n", err)
		}

	case req.Failed:
		fmt.Printf("  AUTH FAIL  %s %s\n", req.Realm, req.Reason)

	case req.StaticChallenge != "":
		fmt.Printf("  2FA        static challenge for %q: %s\n", req.Realm, req.StaticChallenge)
		user := orAsk(username, "  username: ")
		pass := orAsk(password, "  password: ")
		answer := ask("  response: ")
		if err := client.AnswerStaticChallenge(req.Realm, user, pass, answer); err != nil {
			fmt.Printf("  ! %v\n", err)
		}

	case req.NeedUsername:
		fmt.Printf("  AUTH       credentials wanted for %q\n", req.Realm)
		user := orAsk(username, "  username: ")
		pass := orAsk(password, "  password: ")
		if err := client.Credentials(req.Realm, user, pass); err != nil {
			fmt.Printf("  ! %v\n", err)
		}

	default:
		fmt.Printf("  AUTH       passphrase wanted for %q\n", req.Realm)
		pass := orAsk(password, "  passphrase: ")
		if err := client.Password(req.Realm, pass); err != nil {
			fmt.Printf("  ! %v\n", err)
		}
	}
}

// teardown asks openvpn to exit and waits for it, so we learn whether SIGTERM
// over the management interface is a reliable disconnect.
func teardown(client *mgmt.Client, sawConnected, sawBytecount bool) error {
	if err := client.Disconnect(); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	exited := make(chan struct{})
	go func() {
		defer close(exited)
		for range client.Events() {
		}
	}()

	select {
	case <-exited:
		logf("openvpn shut down cleanly")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("openvpn did not exit within 10s of SIGTERM")
	}

	fmt.Println()
	logf("RESULT: pipe protocol OK, management interface OK, clean shutdown OK")
	logf("        reached CONNECTED: %v, saw bytecounts: %v", sawConnected, sawBytecount)
	return nil
}

// --- helpers --------------------------------------------------------------

// freePort asks the OS for an unused loopback port. There is an inherent race
// between releasing it and openvpn binding it; the real implementation should
// retry on failure rather than assume.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// freeUDPPort asks the OS for an unused UDP port for the loopback peer.
func freeUDPPort() (int, error) {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port, nil
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate management password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

var stdin = bufio.NewReader(os.Stdin)

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

func orAsk(value, prompt string) string {
	if value != "" {
		return value
	}
	return ask(prompt)
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

// dumpLog prints the tail of openvpn's own log. When openvpn dies before the
// management interface is up, this is the only account of why.
func dumpLog(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		logf("could not read openvpn log %s: %v", path, err)
		return
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	fmt.Println("\n--- openvpn log (tail) ---")
	for _, l := range lines {
		fmt.Println("  " + l)
	}
	fmt.Println("--- end openvpn log ---")
}

func logf(format string, args ...any) {
	fmt.Printf("[spike] "+format+"\n", args...)
}

// holdsSeen counts openvpn's hold notifications, so the one already released
// during startup is not released a second time.
var holdsSeen int

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
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}
