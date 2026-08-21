//go:build windows

// Package svcpipe speaks the OpenVPN Interactive Service protocol.
//
// The service (OpenVPNServiceInteractive) lets an unprivileged process start
// openvpn.exe with the privileges needed to create tunnel adapters and modify
// routes, without the caller ever being elevated. We connect to its named pipe,
// send a startup message, and it spawns openvpn.exe as:
//
//	openvpn <our options> --msg-channel <handle>
//
// openvpn itself still runs as the invoking user; privileged network operations
// are delegated back to the service over that message channel.
//
// Reference: openvpn/doc/interactive-service-notes.rst and
// openvpn/src/openvpnserv/{interactive,validate}.c
package svcpipe

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PipeName is the well-known pipe published by the interactive service.
const PipeName = `\\.\pipe\openvpn\service`

// maxMessage bounds a single pipe message. The service caps startup data at
// roughly 4096 WCHARs, so this is comfortably above anything it will send back.
const maxMessage = 16 << 10

// Conn is a connection to the interactive service.
type Conn struct {
	h windows.Handle
}

// Dial connects to the interactive service pipe and switches it to message
// mode, which is required: the protocol is message-framed, not a byte stream.
func Dial() (*Conn, error) {
	name, err := windows.UTF16PtrFromString(PipeName)
	if err != nil {
		return nil, err
	}

	var h windows.Handle
	// The service serves a limited number of pipe instances; retry briefly if
	// they are all busy.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h, err = windows.CreateFile(
			name,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, nil,
			windows.OPEN_EXISTING,
			0, 0,
		)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			return nil, fmt.Errorf("connect to %s: %w", PipeName, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	mode := uint32(windows.PIPE_READMODE_MESSAGE)
	if err := windows.SetNamedPipeHandleState(h, &mode, nil, nil); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("set message mode: %w", err)
	}
	return &Conn{h: h}, nil
}

// Close releases the pipe handle. Closing it does not stop openvpn.exe.
func (c *Conn) Close() error {
	if c.h == 0 {
		return nil
	}
	h := c.h
	c.h = 0
	return windows.CloseHandle(h)
}

// Startup is the three-field message the service expects.
type Startup struct {
	// WorkingDir is the directory openvpn.exe is started in. Relative paths in
	// Options (notably --config) resolve against it.
	WorkingDir string
	// Options is the openvpn command line, minus the executable name. Only a
	// small whitelist is permitted for non-admin callers; see white_list[] in
	// openvpnserv/validate.c. Everything else belongs in the .ovpn file.
	Options string
	// Stdin is written to openvpn's standard input. Used to hand over the
	// management-interface password so it never touches disk.
	Stdin string
}

// Send writes the startup message. The service requires it to arrive as a
// single message, so this must be exactly one write.
func (c *Conn) Send(s Startup) error {
	var buf []byte
	for _, field := range []string{s.WorkingDir, s.Options, s.Stdin} {
		enc, err := utf16z(field)
		if err != nil {
			return err
		}
		buf = append(buf, enc...)
	}

	var written uint32
	if err := windows.WriteFile(c.h, buf, &written, nil); err != nil {
		return fmt.Errorf("write startup data: %w", err)
	}
	if int(written) != len(buf) {
		return fmt.Errorf("short write: %d of %d bytes", written, len(buf))
	}
	return nil
}

// Reply is one message from the service. Both success and failure use the same
// three-line shape, distinguished by Code:
//
//	success: "0x00000000\n0x%08x\n%s"  -> code, pid (hex), "Process ID"
//	failure: "0x%08x\n%s\n%s"          -> code, failing function, description
type Reply struct {
	Code uint32
	PID  uint32
	// Func is the failing service function, for Code != 0.
	Func string
	// Text is the human-readable trailer.
	Text string
	Raw  string
}

// OK reports whether the service accepted the request.
func (r Reply) OK() bool { return r.Code == 0 }

func (r Reply) Error() string {
	return fmt.Sprintf("interactive service error 0x%08x in %s: %s", r.Code, r.Func, r.Text)
}

// Recv reads one message. The service writes a reply immediately after the
// startup message, and disconnects the pipe when openvpn.exe exits -- so a
// subsequent Recv returning ErrBrokenPipe is the process-death signal.
func (c *Conn) Recv() (Reply, error) {
	buf := make([]byte, maxMessage)
	var n uint32
	if err := windows.ReadFile(c.h, buf, &n, nil); err != nil {
		return Reply{}, err
	}
	raw := decodeUTF16(buf[:n])
	return parseReply(raw), nil
}

func parseReply(raw string) Reply {
	r := Reply{Raw: raw}
	fields := strings.SplitN(strings.TrimRight(raw, "\x00\n"), "\n", 3)

	if len(fields) > 0 {
		var code uint32
		if _, err := fmt.Sscanf(fields[0], "0x%08x", &code); err == nil {
			r.Code = code
		}
	}
	if len(fields) > 1 {
		if r.Code == 0 {
			var pid uint32
			if _, err := fmt.Sscanf(fields[1], "0x%08x", &pid); err == nil {
				r.PID = pid
			}
		} else {
			r.Func = fields[1]
		}
	}
	if len(fields) > 2 {
		r.Text = fields[2]
	}
	return r
}

// utf16z encodes s as NUL-terminated UTF-16LE.
func utf16z(s string) ([]byte, error) {
	if strings.ContainsRune(s, 0) {
		return nil, errors.New("startup field contains NUL")
	}
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2+2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return append(out, 0, 0), nil
}

func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
	return string(utf16.Decode(units))
}

// ExitEvent is a named Windows event object that openvpn watches when started
// with "--service <name> 0".
//
// Passing --service is not optional for our launch path: without it openvpn
// reads the management password from a console, and the interactive service
// starts it without one, so it dies immediately. With it, openvpn reads the
// password from stdin instead.
//
// Signalling the event is also a shutdown path that does not depend on the
// management connection being healthy.
type ExitEvent struct {
	h    windows.Handle
	name string
}

// NewExitEvent creates a manual-reset event in the unsignalled state. The name
// must be unique per connection; openvpn opens it by name.
func NewExitEvent(name string) (*ExitEvent, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateEvent(nil, 1 /* manual reset */, 0 /* unsignalled */, p)
	if err != nil {
		return nil, fmt.Errorf("create exit event %q: %w", name, err)
	}
	return &ExitEvent{h: h, name: name}, nil
}

// Name is the event name to pass to --service.
func (e *ExitEvent) Name() string { return e.name }

// Signal tells openvpn to exit.
func (e *ExitEvent) Signal() error {
	return windows.SetEvent(e.h)
}

// Close releases the event handle.
func (e *ExitEvent) Close() error {
	if e.h == 0 {
		return nil
	}
	h := e.h
	e.h = 0
	return windows.CloseHandle(h)
}
