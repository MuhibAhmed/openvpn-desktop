//go:build windows

package vpn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/MuhibAhmed/openvpn-desktop/internal/ovpn"
	"github.com/MuhibAhmed/openvpn-desktop/internal/svcpipe"
)

// interactiveServiceName is the Windows service that launches openvpn for us.
const interactiveServiceName = "OpenVPNServiceInteractive"

// windowsRunner starts openvpn through the OpenVPN Interactive Service, so the
// app itself never needs to be elevated and the user never sees a UAC prompt.
//
// See docs/windows-launch-path.md for the protocol and its sharp edges.
type windowsRunner struct{}

// NewRunner returns the launcher for this platform.
func NewRunner() (Runner, error) { return &windowsRunner{}, nil }

// Preflight checks the things that make connecting impossible, in the order a
// person would want to hear about them.
func (r *windowsRunner) Preflight() error {
	install, err := ovpn.Detect()
	if err != nil {
		return err
	}
	if _, err := install.Version(); err != nil {
		return err
	}
	return checkInteractiveService()
}

// checkInteractiveService reports whether the service we depend on is running.
//
// The access rights requested here are deliberately minimal. The obvious
// mgr.Connect() asks for SC_MANAGER_ALL_ACCESS, which a standard user does not
// have, so the check itself fails with "access is denied" on exactly the
// unelevated setup this app is designed for.
func checkInteractiveService() error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("could not query Windows services: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	name, err := windows.UTF16PtrFromString(interactiveServiceName)
	if err != nil {
		return err
	}
	service, err := windows.OpenService(scm, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return fmt.Errorf("the OpenVPN Interactive Service is not installed. " +
			"Reinstall OpenVPN with the service component enabled")
	}
	defer windows.CloseServiceHandle(service)

	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(service, &status); err != nil {
		return fmt.Errorf("could not read the state of the OpenVPN Interactive Service: %w", err)
	}
	if status.CurrentState != windows.SERVICE_RUNNING {
		return fmt.Errorf("the OpenVPN Interactive Service is installed but not running. " +
			"Start it from the Services control panel, or reinstall OpenVPN")
	}
	return nil
}

// Start launches openvpn via the interactive service.
func (r *windowsRunner) Start(ctx context.Context, req StartRequest) (Process, error) {
	if req.ConfigPath == "" {
		return nil, fmt.Errorf("no config file to start")
	}

	token, err := randomToken()
	if err != nil {
		return nil, err
	}

	// --service is mandatory here. Without it openvpn tries to read the
	// management password from a console, and the service starts it without one,
	// so it exits immediately with no useful diagnostic.
	exitEvent, err := svcpipe.NewExitEvent("openvpn-desktop-exit-" + token)
	if err != nil {
		return nil, err
	}

	options := buildOptions(req, exitEvent.Name())

	pipe, err := svcpipe.Dial()
	if err != nil {
		exitEvent.Close()
		return nil, fmt.Errorf("could not reach the OpenVPN Interactive Service: %w", err)
	}

	if err := pipe.Send(svcpipe.Startup{
		WorkingDir: filepath.Dir(req.ConfigPath),
		Options:    options,
		Stdin:      req.ManagementPassword + "\n",
	}); err != nil {
		pipe.Close()
		exitEvent.Close()
		return nil, err
	}

	reply, err := pipe.Recv()
	if err != nil {
		pipe.Close()
		exitEvent.Close()
		return nil, fmt.Errorf("no reply from the OpenVPN Interactive Service: %w", err)
	}
	if !reply.OK() {
		pipe.Close()
		exitEvent.Close()
		return nil, explainServiceError(reply, req.ConfigPath)
	}

	p := &windowsProcess{
		pid:       int(reply.PID),
		pipe:      pipe,
		exitEvent: exitEvent,
		done:      make(chan struct{}),
	}
	// The service disconnects the pipe when openvpn exits, which is our most
	// reliable death signal.
	go p.watch()
	return p, nil
}

// buildOptions assembles the openvpn command line.
//
// Every option here is on the Interactive Service's whitelist
// (openvpnserv/validate.c). Anything else -- routes, DNS, kill-switch related
// options -- has to be written into the .ovpn file instead.
func buildOptions(req StartRequest, exitEventName string) string {
	verb := req.Verbosity
	if verb <= 0 {
		verb = 3
	}
	parts := []string{
		`--config "` + filepath.Base(req.ConfigPath) + `"`,
		fmt.Sprintf("--management %s %d stdin", req.ManagementHost, req.ManagementPort),
		"--management-hold",
		"--management-query-passwords",
		"--auth-retry interact",
		"--service " + exitEventName + " 0",
		fmt.Sprintf("--verb %d", verb),
	}
	if req.LogPath != "" {
		parts = append(parts, `--log "`+req.LogPath+`"`)
	}
	return strings.Join(parts, " ")
}

// explainServiceError turns a service refusal into something actionable. The
// config-directory rule is the one users will actually hit.
func explainServiceError(reply svcpipe.Reply, configPath string) error {
	text := reply.Text + " " + reply.Func
	if strings.Contains(strings.ToLower(text), "admin") {
		return fmt.Errorf("the OpenVPN Interactive Service refused this profile because of where it is stored (%s). "+
			"Profiles must live under a directory the service trusts, and only members of the local Administrators "+
			"group may use other locations: %s", filepath.Dir(configPath), reply.Text)
	}
	return fmt.Errorf("the OpenVPN Interactive Service refused to start openvpn: %s", reply.Error())
}

// windowsProcess is a running openvpn started by the interactive service.
type windowsProcess struct {
	pid       int
	pipe      *svcpipe.Conn
	exitEvent *svcpipe.ExitEvent

	closeOnce sync.Once
	done      chan struct{}
}

func (p *windowsProcess) PID() int              { return p.pid }
func (p *windowsProcess) Done() <-chan struct{} { return p.done }

// watch blocks until the service disconnects the pipe, which it does when
// openvpn exits.
func (p *windowsProcess) watch() {
	for {
		if _, err := p.pipe.Recv(); err != nil {
			p.closeOnce.Do(func() { close(p.done) })
			return
		}
	}
}

// Stop signals the exit event openvpn is watching. This works even if the
// management connection is wedged.
func (p *windowsProcess) Stop() error {
	if err := p.exitEvent.Signal(); err != nil {
		return fmt.Errorf("signal openvpn to exit: %w", err)
	}
	return nil
}

func (p *windowsProcess) Close() error {
	p.exitEvent.Close()
	return p.pipe.Close()
}

func randomToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate a session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
