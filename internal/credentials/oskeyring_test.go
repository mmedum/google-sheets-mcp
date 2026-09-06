package credentials

import "testing"

// The production backend is a thin wrapper over go-keyring. This asserts
// it is wired to the library at all — the calls themselves need a
// desktop session, which CI does not have.
func TestOSKeyringIsWired(t *testing.T) {
	b := OSKeyring()
	if b == nil {
		t.Fatal("OSKeyring returned nil")
	}
	_, err := b.Get(ServiceName, "no-such-profile-"+t.Name())
	if err == nil {
		t.Skip("a keyring entry exists for a profile that should not exist")
	}
	// Either "not found" or "no keyring here"; both are answers, and
	// both fall through to the file in ResolveStored.
	_ = IsKeyringNotFound(err)
}
