package vpn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MuhibAhmed/openvpn-desktop/internal/profile"
)

// Manager owns the one active connection.
//
// A single tunnel at a time is a deliberate limit, not a simplification we will
// regret: openvpn's default route handling means two simultaneous tunnels fight
// over the same default gateway, and the UI has no honest way to show that.
type Manager struct {
	runner Runner
	logDir string
	lookup SecretLookup

	mu      sync.Mutex
	session *Session
	last    Status

	handlersMu sync.RWMutex
	onStatus   []func(Status)
	onLog      []func(LogLine)
}

// NewManager returns a manager that launches tunnels with runner, writes
// openvpn's own logs into logDir, and answers credential prompts from lookup
// when something has already been saved. lookup may be nil, in which case every
// prompt reaches the user.
func NewManager(runner Runner, logDir string, lookup SecretLookup) (*Manager, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", logDir, err)
	}
	return &Manager{
		runner: runner,
		logDir: logDir,
		lookup: lookup,
		last: Status{
			Phase:  PhaseIdle,
			Detail: "Not connected",
			Since:  time.Now(),
		},
	}, nil
}

// OnStatus registers a status listener. Listeners are called in registration
// order from the session's goroutine, so they must not block.
func (m *Manager) OnStatus(fn func(Status)) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	m.onStatus = append(m.onStatus, fn)
}

// OnLog registers a log listener.
func (m *Manager) OnLog(fn func(LogLine)) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	m.onLog = append(m.onLog, fn)
}

// Preflight reports whether connecting is possible at all.
func (m *Manager) Preflight() error { return m.runner.Preflight() }

// Status returns the current connection state, which is the idle state when
// nothing is running.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil {
		return m.session.Status()
	}
	return m.last
}

// Logs returns the active session's log lines, or nothing when idle.
func (m *Manager) Logs() []LogLine {
	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Logs()
}

// Connect brings up p, replacing any existing connection.
//
// It returns once openvpn is running and subscribed; watch the status stream
// for what happens after that. Failures that happen during startup are returned
// here *and* published as a failed status, because the caller may be a UI action
// or a tray click and both want to hear about it.
func (m *Manager) Connect(ctx context.Context, p *profile.Profile) error {
	if p == nil {
		return fmt.Errorf("no profile to connect to")
	}
	if err := m.runner.Preflight(); err != nil {
		m.publishStatus(Status{
			Phase:       PhaseFailed,
			ProfileID:   p.ID,
			ProfileName: p.Name,
			Detail:      err.Error(),
			Error:       err.Error(),
			Since:       time.Now(),
		})
		return err
	}

	// Tear down whatever is running first, and wait for it: two openvpn
	// processes competing for the same adapter fail in ways that are hard to
	// explain to a user.
	if err := m.disconnectAndWait(10 * time.Second); err != nil {
		return err
	}

	session := newSession(m.runner, SessionConfig{
		ProfileID:   p.ID,
		ProfileName: p.Name,
		ConfigPath:  p.ConfigPath(),
		LogPath:     filepath.Join(m.logDir, p.ID+".log"),
		Verbosity:   3,
	}, Handlers{
		OnStatus:     m.publishStatus,
		OnLog:        m.publishLog,
		LookupSecret: m.lookup,
	})

	m.mu.Lock()
	m.session = session
	m.mu.Unlock()

	// Forget the session once it ends, so Status falls back to the last
	// published state instead of a corpse.
	go func() {
		<-session.Done()
		m.mu.Lock()
		if m.session == session {
			m.last = session.Status()
			m.session = nil
		}
		m.mu.Unlock()
	}()

	if err := session.start(ctx, 3); err != nil {
		return err
	}
	return nil
}

// Disconnect brings down the active connection. It is not an error to call it
// when nothing is connected.
func (m *Manager) Disconnect() error {
	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Disconnect()
}

// disconnectAndWait stops the active session and waits for it to finish.
func (m *Manager) disconnectAndWait(timeout time.Duration) error {
	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if session == nil {
		return nil
	}

	if err := session.Disconnect(); err != nil {
		return fmt.Errorf("could not stop the current connection: %w", err)
	}
	select {
	case <-session.Done():
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("the previous connection did not shut down; try again in a moment")
	}
}

// Answer supplies credentials for whatever the active session is waiting on.
func (m *Manager) Answer(a Answer) error {
	m.mu.Lock()
	session := m.session
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("nothing is waiting for credentials")
	}
	return session.Answer(a)
}

func (m *Manager) publishStatus(st Status) {
	m.mu.Lock()
	m.last = st
	m.mu.Unlock()

	m.handlersMu.RLock()
	listeners := m.onStatus
	m.handlersMu.RUnlock()
	for _, fn := range listeners {
		fn(st)
	}
}

func (m *Manager) publishLog(line LogLine) {
	m.handlersMu.RLock()
	listeners := m.onLog
	m.handlersMu.RUnlock()
	for _, fn := range listeners {
		fn(line)
	}
}
