//go:build windows

package creds

import (
	"fmt"
	"strings"
	"syscall"

	"github.com/danieljoos/wincred"
)

// targetPrefix namespaces our entries in the Windows Credential Manager, so
// they are recognisable in control panel and cannot collide with another app's.
const targetPrefix = "VPNDesktop:"

// windowsStore keeps credentials as generic entries in the Credential Manager.
type windowsStore struct{}

// NewStore returns the credential store for this platform.
func NewStore() (Store, error) { return &windowsStore{}, nil }

// target names the vault entry. The auth slot keeps the bare profile id, so
// credentials saved before slots existed are still found.
func target(profileID string, slot Slot) string {
	if slot == SlotAuth {
		return targetPrefix + profileID
	}
	return targetPrefix + profileID + "#" + string(slot)
}

func (s *windowsStore) Get(profileID string, slot Slot) (Credentials, error) {
	if profileID == "" {
		return Credentials{}, ErrNotFound
	}
	cred, err := wincred.GetGenericCredential(target(profileID, slot))
	if err != nil {
		// The API reports a missing entry as ERROR_NOT_FOUND; treat anything
		// that looks like absence as absence rather than as a failure, so the
		// UI does not show an error for the normal first-run case.
		if isNotFound(err) {
			return Credentials{}, ErrNotFound
		}
		return Credentials{}, fmt.Errorf("read saved credentials: %w", err)
	}
	return Credentials{
		Username: cred.UserName,
		Password: string(decodeUTF16(cred.CredentialBlob)),
	}, nil
}

func (s *windowsStore) Save(profileID string, slot Slot, c Credentials) error {
	if profileID == "" {
		return fmt.Errorf("cannot save credentials without a profile")
	}
	cred := wincred.NewGenericCredential(target(profileID, slot))
	cred.UserName = c.Username
	cred.CredentialBlob = encodeUTF16(c.Password)
	// Persist across reboots; the whole point is not asking again.
	cred.Persist = wincred.PersistLocalMachine
	if err := cred.Write(); err != nil {
		return fmt.Errorf("save credentials to Windows Credential Manager: %w", err)
	}
	return nil
}

func (s *windowsStore) Delete(profileID string, slot Slot) error {
	if profileID == "" {
		return nil
	}
	cred, err := wincred.GetGenericCredential(target(profileID, slot))
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("look up saved credentials: %w", err)
	}
	if err := cred.Delete(); err != nil {
		return fmt.Errorf("delete saved credentials: %w", err)
	}
	return nil
}

// Forget removes every secret a profile might have saved.
func (s *windowsStore) Forget(profileID string) error {
	for _, slot := range AllSlots() {
		if err := s.Delete(profileID, slot); err != nil {
			return err
		}
	}
	return nil
}

func isNotFound(err error) bool {
	var errno syscall.Errno
	if e, ok := err.(syscall.Errno); ok {
		errno = e
	}
	// ERROR_NOT_FOUND
	if errno == 1168 {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "cannot be found") ||
		strings.Contains(strings.ToLower(err.Error()), "not found")
}

// The Credential Manager stores blobs as raw bytes. Other Windows credential
// consumers write passwords as UTF-16, and matching that keeps our entries
// readable in the control panel rather than showing as mojibake.

func encodeUTF16(s string) []byte {
	units := syscall.StringToUTF16(s)
	// Drop the trailing NUL: the blob length carries the size.
	if n := len(units); n > 0 {
		units = units[:n-1]
	}
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func decodeUTF16(b []byte) []byte {
	if len(b) < 2 {
		return b
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return []byte(string(syscall.UTF16ToString(units)))
}
