package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dropped builds a directory that looks like what a provider hands out: a
// config plus the files it references.
func dropped(t *testing.T, configName, config string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const sidecarConfig = `client
dev tun
proto udp
remote vpn.example.com 1194
auth-user-pass
ca ca.crt
cert client.crt
key client.key
tls-auth ta.key 1
`

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImportProducesSelfContainedProfile(t *testing.T) {
	src := dropped(t, "US East.ovpn", sidecarConfig, map[string]string{
		"ca.crt":     "CA-BODY",
		"client.crt": "CERT-BODY",
		"client.key": "KEY-BODY",
		"ta.key":     "TA-BODY",
	})
	store := newStore(t)

	p, err := store.Import(filepath.Join(src, "US East.ovpn"))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.ID != "us-east" {
		t.Errorf("ID = %q, want a slug of the filename", p.ID)
	}
	if p.Name != "US East" {
		t.Errorf("Name = %q, want the original filename preserved for display", p.Name)
	}
	if !p.Summary.NeedsCredentials {
		t.Error("NeedsCredentials = false, want true")
	}
	if len(p.Summary.Remotes) != 1 || p.Summary.Remotes[0] != "vpn.example.com:1194" {
		t.Errorf("Remotes = %v", p.Summary.Remotes)
	}

	// The stored config must stand on its own: every referenced file inlined,
	// nothing pointing back at the directory the user dropped from.
	data, err := os.ReadFile(p.ConfigPath())
	if err != nil {
		t.Fatalf("read stored config: %v", err)
	}
	stored := string(data)
	for _, want := range []string{"CA-BODY", "CERT-BODY", "KEY-BODY", "TA-BODY", "key-direction 1"} {
		if !strings.Contains(stored, want) {
			t.Errorf("stored config is missing %q\n%s", want, stored)
		}
	}
	for _, unwanted := range []string{"ca.crt", "client.key", "ta.key"} {
		if strings.Contains(stored, unwanted) {
			t.Errorf("stored config still references the external file %q", unwanted)
		}
	}

	// Deleting the source must not break the profile.
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.ConfigPath()); err != nil {
		t.Errorf("profile config vanished with its source: %v", err)
	}
}

func TestImportTwiceDoesNotCollide(t *testing.T) {
	store := newStore(t)
	src := dropped(t, "office.ovpn", "client\nremote a.example.com 1194\n<ca>\nCA\n</ca>\n", nil)

	first, err := store.Import(filepath.Join(src, "office.ovpn"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Import(filepath.Join(src, "office.ovpn"))
	if err != nil {
		t.Fatal(err)
	}

	if first.ID == second.ID {
		t.Fatalf("both imports got id %q; the second must not overwrite the first", first.ID)
	}
	if second.ID != "office-2" {
		t.Errorf("second ID = %q, want office-2", second.ID)
	}
	for _, p := range []*Profile{first, second} {
		if _, err := os.Stat(p.ConfigPath()); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}

func TestListRenameDelete(t *testing.T) {
	store := newStore(t)
	src := dropped(t, "home.ovpn", "client\nremote a.example.com 1194\n<ca>\nCA\n</ca>\n", nil)
	p, err := store.Import(filepath.Join(src, "home.ovpn"))
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("List = %v, want the imported profile", list)
	}
	// Metadata must survive the round trip through disk.
	if len(list[0].Summary.Remotes) != 1 {
		t.Errorf("listed profile lost its summary: %+v", list[0].Summary)
	}

	if _, err := store.Rename(p.ID, "Home office"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	reloaded, err := store.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "Home office" {
		t.Errorf("Name = %q after rename", reloaded.Name)
	}

	if err := store.Delete(p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list, err := store.List(); err != nil || len(list) != 0 {
		t.Errorf("List after delete = %v (err %v), want empty", list, err)
	}
}

func TestDeleteRejectsPathEscape(t *testing.T) {
	store := newStore(t)
	for _, id := range []string{"..", "../evil", `..\evil`, "a/b", ""} {
		if err := store.Delete(id); err == nil {
			t.Errorf("Delete(%q) succeeded, want it rejected", id)
		}
	}
}

func TestImportPathsHandlesAMixedDrop(t *testing.T) {
	src := dropped(t, "one.ovpn", "client\nremote a.example.com 1194\n<ca>\nCA\n</ca>\n", map[string]string{
		"notes.txt": "hello",
		"two.ovpn":  "client\nremote b.example.com 1194\n<ca>\nCA\n</ca>\n",
	})
	store := newStore(t)

	imported, errs := store.ImportPaths([]string{
		filepath.Join(src, "one.ovpn"),
		filepath.Join(src, "two.ovpn"),
		filepath.Join(src, "notes.txt"),
	})

	if len(imported) != 2 {
		t.Errorf("imported %d profiles, want 2", len(imported))
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one about the non-profile file", errs)
	}
	if !strings.Contains(errs[0].Error(), "notes.txt") {
		t.Errorf("error = %v, want it to name notes.txt", errs[0])
	}
}

func TestImportPathsAcceptsADroppedDirectory(t *testing.T) {
	src := dropped(t, "provider.ovpn", sidecarConfig, map[string]string{
		"ca.crt":     "CA",
		"client.crt": "CERT",
		"client.key": "KEY",
		"ta.key":     "TA",
	})
	store := newStore(t)

	imported, errs := store.ImportPaths([]string{src})
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if len(imported) != 1 {
		t.Fatalf("imported %d, want 1", len(imported))
	}
	data, err := os.ReadFile(imported[0].ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "CA") {
		t.Error("certificates from the dropped directory were not inlined")
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.ovpn")
	if err := os.WriteFile(path, []byte("<ca>\nunterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newStore(t)
	if _, err := store.Import(path); err == nil {
		t.Fatal("Import succeeded on a malformed config, want an error")
	}
	// A failed import must not leave a directory behind.
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("List = %v after a failed import, want empty", list)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"US East":        "us-east",
		"vpn.example":    "vpn-example",
		"  spaced  out ": "spaced-out",
		"Ünïcode":        "ünïcode",
		"---":            "",
		"a__b":           "a-b",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
