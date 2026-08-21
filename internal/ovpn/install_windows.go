//go:build windows

// Package ovpn locates and describes the OpenVPN installation the app drives.
//
// We do not reimplement OpenVPN; we drive the community build. The installer
// puts it on disk and this package is how the rest of the app finds it, decides
// whether it is usable, and learns the directories the Interactive Service will
// accept configs from.
package ovpn

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registryKey is where the OpenVPN MSI records the installation layout. The
// Interactive Service reads the same values, so what we find here is exactly
// what it will enforce.
const registryKey = `SOFTWARE\OpenVPN`

// Install describes a detected OpenVPN installation.
type Install struct {
	// Root is the installation directory.
	Root string
	// ExePath is openvpn.exe. The Interactive Service will only launch this
	// exact binary, so we cannot substitute our own.
	ExePath string
	// ConfigDir is the global config directory. For callers who are not members
	// of AdminGroup, the service accepts config files only from inside this
	// directory or a subdirectory of it.
	ConfigDir string
	// LogDir is the directory the MSI designated for logs.
	LogDir string
	// ConfigExt is the config file extension the GUI convention uses ("ovpn").
	ConfigExt string
	// AdminGroup is the group whose members may pass unrestricted options.
	AdminGroup string
}

// Detect reads the installation layout from the registry. It returns an error
// if OpenVPN is not installed, which is the app's cue to run the bundled
// installer.
func Detect() (*Install, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, registryKey, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("OpenVPN is not installed (registry key HKLM\\%s missing): %w", registryKey, err)
	}
	defer key.Close()

	get := func(name string) string {
		v, _, err := key.GetStringValue(name)
		if err != nil {
			return ""
		}
		return strings.TrimRight(v, `\`)
	}

	in := &Install{
		Root:       get(""),
		ExePath:    get("exe_path"),
		ConfigDir:  get("config_dir"),
		LogDir:     get("log_dir"),
		ConfigExt:  get("config_ext"),
		AdminGroup: get("ovpn_admin_group"),
	}
	if in.ConfigExt == "" {
		in.ConfigExt = "ovpn"
	}
	if in.ExePath == "" {
		return nil, fmt.Errorf("OpenVPN registry key has no exe_path value")
	}
	if _, err := os.Stat(in.ExePath); err != nil {
		return nil, fmt.Errorf("openvpn.exe recorded at %s is missing: %w", in.ExePath, err)
	}
	return in, nil
}

// Version runs openvpn.exe to read its version banner, e.g.
// "OpenVPN 2.6.11 Windows-MSVC [SSL (OpenSSL)] ...". This is how we decide
// whether a pre-existing install is new enough to use.
func (in *Install) Version() (string, error) {
	out, err := exec.Command(in.ExePath, "--version").CombinedOutput()
	// openvpn --version exits non-zero by design, so only trust the output.
	if len(out) == 0 && err != nil {
		return "", fmt.Errorf("run %s --version: %w", in.ExePath, err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	if sc.Scan() {
		return strings.TrimSpace(sc.Text()), nil
	}
	return "", fmt.Errorf("openvpn produced no version output")
}

// UserConfigDir is the per-user profile directory the community GUI uses. The
// Interactive Service accepts configs from here as well as from ConfigDir,
// which matters because a standard user cannot write into Program Files.
func UserConfigDir() string {
	return filepath.Join(os.Getenv("USERPROFILE"), "OpenVPN", "config")
}

// HasWintun reports whether this install shipped the Wintun driver. OpenVPN
// 2.4 and 2.5 builds may not, in which case configs must not ask for it.
func (in *Install) HasWintun() bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(in.ExePath), "wintun.dll"))
	return err == nil
}

// GenKey writes a new static key to path using openvpn's own generator. Used
// by tests and diagnostics that need a working tunnel without a PKI.
func (in *Install) GenKey(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := exec.Command(in.ExePath, "--genkey", "secret", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("openvpn --genkey: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
