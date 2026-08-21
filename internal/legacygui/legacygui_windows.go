//go:build windows

// Package legacygui reads credentials that the OpenVPN community GUI has
// already saved.
//
// This exists because of a genuine trap. A profile can need both an account
// password and a private key passphrase, and the old GUI only prompts for the
// ones it has not saved. Someone who ticked "save" on the key passphrase years
// ago is asked for one secret and believes that is all their profile needs --
// the passphrase is supplied silently from the registry and they may never have
// known it. Replacing that GUI without reading what it remembers would mean
// asking people for a secret they have no way to produce.
//
// Everything here is the user's own credential, on their own machine, encrypted
// by Windows against their own login. We only ever read it.
package legacygui

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// registryRoot is where the community GUI keeps its per-profile settings.
const registryRoot = `Software\OpenVPN-GUI\configs`

// Value names, from openvpn-gui's save_pass.c.
const (
	valueEntropy     = "entropy"
	valueAuthPass    = "auth-data"
	valueKeyPass     = "key-data"
	valueUsernameEnc = "username-enc"
	// valueUsername is the older plaintext form, still present on machines that
	// have been upgraded rather than reinstalled.
	valueUsername = "username"
)

// ErrNotFound reports that the community GUI has nothing saved for a profile.
var ErrNotFound = errors.New("no saved credentials in the OpenVPN GUI")

// Saved is whatever the community GUI had stored for one profile. Any field may
// be empty; the Has flags distinguish "empty" from "absent".
type Saved struct {
	Username      string
	AuthPassword  string
	KeyPassphrase string

	HasUsername      bool
	HasAuthPassword  bool
	HasKeyPassphrase bool
}

// Empty reports whether nothing at all was found.
func (s Saved) Empty() bool {
	return !s.HasUsername && !s.HasAuthPassword && !s.HasKeyPassphrase
}

// Lookup returns what the community GUI saved for a profile.
//
// configName is the profile name as that GUI knows it, which is the .ovpn file
// name without its extension.
func Lookup(configName string) (Saved, error) {
	if configName == "" {
		return Saved{}, ErrNotFound
	}

	key, err := registry.OpenKey(registry.CURRENT_USER, registryRoot+`\`+configName, registry.QUERY_VALUE)
	if err != nil {
		return Saved{}, ErrNotFound
	}
	defer key.Close()

	// The entropy is shared by every encrypted value for this profile. Without
	// it nothing decrypts, but its absence is normal for a profile whose
	// credentials were never saved.
	entropy, _, err := key.GetBinaryValue(valueEntropy)
	if err != nil {
		entropy = nil
	}

	var saved Saved

	// A username is not a secret, and the GUI has stored it three different ways
	// over the years: encrypted, as a raw UTF-16 buffer, and as a plain string.
	if v, err := decryptValue(key, valueUsernameEnc, entropy); err == nil {
		saved.Username, saved.HasUsername = v, true
	} else if raw, _, err := key.GetBinaryValue(valueUsername); err == nil {
		saved.Username, saved.HasUsername = decodeUTF16(raw), true
	} else if v, _, err := key.GetStringValue(valueUsername); err == nil {
		saved.Username, saved.HasUsername = v, true
	}

	if v, err := decryptValue(key, valueAuthPass, entropy); err == nil {
		saved.AuthPassword, saved.HasAuthPassword = v, true
	}
	if v, err := decryptValue(key, valueKeyPass, entropy); err == nil {
		saved.KeyPassphrase, saved.HasKeyPassphrase = v, true
	}

	if saved.Empty() {
		return Saved{}, ErrNotFound
	}
	return saved, nil
}

// decryptValue reads one DPAPI-protected registry value.
func decryptValue(key registry.Key, name string, entropy []byte) (string, error) {
	blob, _, err := key.GetBinaryValue(name)
	if err != nil {
		return "", err
	}
	if len(blob) == 0 {
		return "", ErrNotFound
	}
	plain, err := unprotect(blob, entropy)
	if err != nil {
		return "", err
	}
	return decodeUTF16(plain), nil
}

// unprotect reverses CryptProtectData.
//
// The entropy must be passed exactly as the GUI passed it: openvpn-gui stores
// ENTROPY_LEN+1 bytes but builds the blob with strlen(), so the trailing NUL is
// excluded. Getting that length wrong fails decryption with nothing to explain
// why, so the C string length is computed rather than assumed.
func unprotect(blob, entropy []byte) ([]byte, error) {
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}

	var entropyBlob *windows.DataBlob
	if n := cStringLen(entropy); n > 0 {
		entropyBlob = &windows.DataBlob{Size: uint32(n), Data: &entropy[0]}
	}

	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, entropyBlob, 0, nil, 0, &out); err != nil {
		return nil, fmt.Errorf("decrypt saved credential: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	// Copy before the buffer is freed.
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

// cStringLen returns the length up to the first NUL, matching strlen.
func cStringLen(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

// decodeUTF16 turns the stored WCHAR string into Go text, dropping the
// terminating NUL that openvpn-gui includes in the protected data.
func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u := uint16(b[i]) | uint16(b[i+1])<<8
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}
