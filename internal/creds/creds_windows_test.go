//go:build windows

package creds

import "testing"

// These tests exercise the real Windows Credential Manager. They use a
// namespaced test id and remove their entries afterwards, because there is no
// meaningful way to verify this code against a fake: the encoding only matters
// insofar as Windows agrees with it.
const testProfile = "__selftest__"

func TestStoreRoundTrip(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Forget(testProfile) })
	store.Forget(testProfile) // clear anything left by an interrupted run

	want := Credentials{Username: "alice@example.com", Password: "pa ss\"wörd\\1"}
	if err := store.Save(testProfile, SlotAuth, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(testProfile, SlotAuth)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the credentials\n got %+v\nwant %+v", got, want)
	}

	if err := store.Delete(testProfile, SlotAuth); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(testProfile, SlotAuth); err != ErrNotFound {
		t.Errorf("after Delete, Get returned %v, want ErrNotFound", err)
	}
	// Deleting twice must be harmless, so callers can tidy up unconditionally.
	if err := store.Delete(testProfile, SlotAuth); err != nil {
		t.Errorf("second Delete: %v, want nil", err)
	}
}

// TestSlotsAreIndependent matters because a profile commonly needs both an
// account password and a private key passphrase, and confusing the two would
// send the wrong secret to openvpn.
func TestSlotsAreIndependent(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Forget(testProfile) })
	store.Forget(testProfile)

	auth := Credentials{Username: "alice", Password: "account-secret"}
	key := Credentials{Password: "key-passphrase"}

	if err := store.Save(testProfile, SlotAuth, auth); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(testProfile, SlotPrivateKey, key); err != nil {
		t.Fatal(err)
	}

	gotAuth, err := store.Get(testProfile, SlotAuth)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != auth {
		t.Errorf("auth slot = %+v, want %+v", gotAuth, auth)
	}

	gotKey, err := store.Get(testProfile, SlotPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey.Password != key.Password {
		t.Errorf("key slot password = %q, want %q", gotKey.Password, key.Password)
	}

	// Removing one must not remove the other.
	if err := store.Delete(testProfile, SlotAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(testProfile, SlotPrivateKey); err != nil {
		t.Errorf("deleting the auth slot also removed the key slot: %v", err)
	}

	if err := store.Forget(testProfile); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	for _, slot := range AllSlots() {
		if _, err := store.Get(testProfile, slot); err != ErrNotFound {
			t.Errorf("slot %q survived Forget: %v", slot, err)
		}
	}
}

// TestSaveUsernameWithoutPassword covers servers that only want a username.
// openvpn supports a blank password, so we have to be able to store one.
func TestSaveUsernameWithoutPassword(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Forget(testProfile) })
	store.Forget(testProfile)

	want := Credentials{Username: "alice", Password: ""}
	if err := store.Save(testProfile, SlotAuth, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Get(testProfile, SlotAuth)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetUnknownProfile(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("__definitely_not_saved__", SlotAuth); err != ErrNotFound {
		t.Errorf("Get on an unknown profile = %v, want ErrNotFound", err)
	}
	if _, err := store.Get("", SlotAuth); err != ErrNotFound {
		t.Errorf("Get on an empty id = %v, want ErrNotFound", err)
	}
}

func TestEmpty(t *testing.T) {
	if !(Credentials{}).Empty() {
		t.Error("zero Credentials should be Empty")
	}
	if (Credentials{Username: "alice"}).Empty() {
		t.Error("a username alone is worth saving")
	}
	if (Credentials{Password: "x"}).Empty() {
		t.Error("a password alone is worth saving")
	}
}
