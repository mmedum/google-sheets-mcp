package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeKeyring is an in-memory Backend that can be made to fail the way a
// headless machine's does.
type fakeKeyring struct {
	items  map[string]string
	getErr error
	setErr error
	delErr error
}

func newFake() *fakeKeyring { return &fakeKeyring{items: map[string]string{}} }

func (f *fakeKeyring) Get(service, account string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.items[service+"/"+account]
	if !ok {
		return "", errNotInKeyring
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, account, secret string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.items[service+"/"+account] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, account string) error {
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.items, service+"/"+account)
	return nil
}

var errNotInKeyring = errors.New("secret not found in keyring")

func newStore(t *testing.T, kr Backend, env map[string]string) *Store {
	t.Helper()
	var warnings []string
	s := &Store{
		Profile:  "default",
		Keyring:  kr,
		FilePath: filepath.Join(t.TempDir(), "token.json"),
		Env:      func(k string) string { return env[k] },
		Warn:     func(m string) { warnings = append(warnings, m) },
	}
	t.Cleanup(func() { _ = warnings })
	return s
}

func TestEnvironmentWinsAndIsNotDeletable(t *testing.T) {
	kr := newFake()
	s := newStore(t, kr, map[string]string{EnvVar: "env-token"})
	if _, err := s.Save("stored-token"); err != nil {
		t.Fatal(err)
	}

	tok, src, err := s.Resolve()
	if err != nil || tok != "env-token" || src != SourceEnv {
		t.Fatalf("Resolve = %q, %q, %v; want the environment override", tok, src, err)
	}
	// logout revokes what it stored, not what the environment supplies:
	// deleting a token this process does not own would be a surprise.
	tok, src, err = s.ResolveStored()
	if err != nil || tok != "stored-token" || src != SourceKeyring {
		t.Fatalf("ResolveStored = %q, %q, %v", tok, src, err)
	}
}

func TestKeyringThenFile(t *testing.T) {
	kr := newFake()
	s := newStore(t, kr, nil)

	if _, _, err := s.Resolve(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve with nothing stored = %v", err)
	}
	if !strings.Contains(ErrNotFound.Error(), "login") {
		t.Errorf("ErrNotFound does not name the fix: %v", ErrNotFound)
	}

	src, err := s.Save("tok")
	if err != nil || src != SourceKeyring {
		t.Fatalf("Save = %q, %v; want the keyring", src, err)
	}
	if _, err := os.Stat(s.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("a keyring save must not leave a plaintext copy")
	}

	// A machine with no secret service: the token still has to land
	// somewhere, and the person has to be told where.
	var warnings []string
	s.Warn = func(m string) { warnings = append(warnings, m) }
	kr.setErr = errors.New("no dbus session")
	kr.getErr = errors.New("no dbus session")
	src, err = s.Save("tok2")
	if err != nil || src != SourceFile {
		t.Fatalf("Save without a keyring = %q, %v; want the file", src, err)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "plaintext") {
		t.Errorf("no plaintext warning: %v", warnings)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("token file mode is %v, want 0600", perm)
		}
	}

	warnings = nil
	tok, src, err := s.Resolve()
	if err != nil || tok != "tok2" || src != SourceFile {
		t.Fatalf("Resolve from file = %q, %q, %v", tok, src, err)
	}
	if len(warnings) == 0 {
		t.Error("reading a plaintext token warns on every use, not once at login")
	}
}

func TestSaveRefusesEmptyAndDeleteClearsBoth(t *testing.T) {
	kr := newFake()
	s := newStore(t, kr, nil)
	if _, err := s.Save(""); err == nil {
		t.Error("an empty token must be refused; it would look like a login that worked")
	}

	if _, err := s.Save("tok"); err != nil {
		t.Fatal(err)
	}
	kr.setErr = errors.New("gone")
	if _, err := s.Save("tok"); err != nil {
		t.Fatal(err)
	}
	kr.setErr = nil
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(s.FilePath); !errors.Is(err, os.ErrNotExist) {
		t.Error("Delete left the token file behind")
	}
	if len(kr.items) != 0 {
		t.Error("Delete left the keyring entry behind")
	}
	if err := s.Delete(); err != nil {
		t.Fatalf("Delete twice should be fine: %v", err)
	}
}

func TestBrokenKeyringErrorSurvivesToTheEnd(t *testing.T) {
	kr := newFake()
	kr.getErr = errors.New("no secret service")
	s := newStore(t, kr, nil)
	_, _, err := s.Resolve()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "no secret service") {
		t.Errorf("the keyring's own failure is lost: %v", err)
	}
}

func TestFileWithBrokenJSON(t *testing.T) {
	s := newStore(t, nil, nil)
	if err := os.WriteFile(s.FilePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Resolve(); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve on a broken token file = %v; want a parse error", err)
	}
}

func TestNoStoreConfigured(t *testing.T) {
	s := &Store{Profile: "default"}
	if _, err := s.Save("tok"); err == nil {
		t.Error("saving with neither a keyring nor a file must fail loudly")
	}
}
