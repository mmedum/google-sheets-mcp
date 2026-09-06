package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProfileLayout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	base, err := BaseDir()
	if err != nil || base != dir {
		t.Fatalf("BaseDir() = %q, %v; want the override", base, err)
	}

	got, err := ProfileDir(DefaultProfile)
	if err != nil || got != dir {
		t.Errorf("the default profile lives at the base: %q, %v", got, err)
	}
	got, err = ProfileDir("")
	if err != nil || got != dir {
		t.Errorf("an unnamed profile is the default: %q, %v", got, err)
	}
	got, err = ProfileDir("work")
	if err != nil || got != filepath.Join(dir, "profiles", "work") {
		t.Errorf("ProfileDir(work) = %q, %v", got, err)
	}

	// Two profiles never share a secret, a token or an account.
	for _, f := range []func(string) (string, error){Path, DefaultClientSecretPath, TokenFilePath} {
		a, err := f("default")
		if err != nil {
			t.Fatal(err)
		}
		b, err := f("work")
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Errorf("two profiles share %s", a)
		}
	}
}

func TestSaveLoadRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	if _, err := Load("work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before Save = %v, want ErrNotFound", err)
	}
	// The message tells the person what to run, because this is the
	// first thing anybody hits.
	if !strings.Contains(ErrNotFound.Error(), "login") {
		t.Errorf("ErrNotFound does not name the fix: %v", ErrNotFound)
	}

	want := Config{
		ClientSecretPath: filepath.Join(dir, "client_secret.json"),
		AccountEmail:     "someone@example.test",
		TokenStore:       "keyring",
		Scopes:           []string{"https://www.googleapis.com/auth/spreadsheets"},
	}
	if err := Save("work", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load("work")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccountEmail != want.AccountEmail || got.TokenStore != want.TokenStore ||
		len(got.Scopes) != 1 || got.Scopes[0] != want.Scopes[0] {
		t.Errorf("round trip gave %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("Save should stamp UpdatedAt")
	}

	p, err := Path("work")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("config file mode is %v, want 0600", perm)
		}
	}

	if err := Remove("work"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := Remove("work"); err != nil {
		t.Fatalf("Remove twice should be fine: %v", err)
	}
	if _, err := Load("work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after Remove = %v", err)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	p, err := Path(DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(DefaultProfile); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Load on broken JSON = %v; want a parse error naming the file", err)
	}
}

func TestBaseDirFallsBackToTheUserConfigDir(t *testing.T) {
	t.Setenv(EnvDir, "")
	got, err := BaseDir()
	if err != nil {
		t.Fatalf("BaseDir: %v", err)
	}
	want, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("this machine has no user config dir: %v", err)
	}
	if got != filepath.Join(want, AppDir) {
		t.Errorf("BaseDir() = %q, want %q", got, filepath.Join(want, AppDir))
	}
	// Every path helper is built on it, so they all move together.
	for _, f := range []func(string) (string, error){Path, DefaultClientSecretPath, TokenFilePath, ProfileDir} {
		p, err := f("default")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(p, got) {
			t.Errorf("%q is not under the base directory %q", p, got)
		}
	}
}

func TestSaveReportsAnUnwritableProfileDirectory(t *testing.T) {
	dir := t.TempDir()
	// A file where the profile directory has to go: MkdirAll fails, and
	// the error has to name the path rather than come back as a bare
	// "not a directory".
	blocked := filepath.Join(dir, "profiles")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, dir)

	err := Save("work", Config{AccountEmail: "someone@example.test"})
	if err == nil {
		t.Fatal("Save into an unwritable directory reported success")
	}
	if !strings.Contains(err.Error(), "userconfig:") {
		t.Errorf("the error does not say which package failed: %v", err)
	}
	if _, err := Load("work"); err == nil {
		t.Error("Load found a config that was never written")
	}
}

func TestRemoveReportsARealFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	p, err := Path(DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	// A directory where the config file should be: Remove fails with
	// something other than "not there", and that has to surface.
	if err := os.MkdirAll(filepath.Join(p, "in-the-way"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Remove(DefaultProfile); err == nil {
		t.Error("Remove over a non-empty directory reported success")
	}
}

func TestNoUserConfigDirIsReportedNotGuessed(t *testing.T) {
	// With nowhere to put a profile, every path helper has to say so
	// rather than fall back to the working directory, where a token file
	// could end up in somebody's repository.
	t.Setenv(EnvDir, "")
	for _, key := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA", "USERPROFILE"} {
		t.Setenv(key, "")
	}
	if _, err := os.UserConfigDir(); err == nil {
		t.Skip("this platform still finds a config directory with the environment cleared")
	}
	for name, f := range map[string]func(string) (string, error){
		"BaseDir":                 func(string) (string, error) { return BaseDir() },
		"ProfileDir":              ProfileDir,
		"Path":                    Path,
		"DefaultClientSecretPath": DefaultClientSecretPath,
		"TokenFilePath":           TokenFilePath,
	} {
		if got, err := f("work"); err == nil {
			t.Errorf("%s returned %q with no config directory available", name, got)
		}
	}
	if _, err := Load("work"); err == nil {
		t.Error("Load succeeded with no config directory")
	}
	if err := Save("work", Config{}); err == nil {
		t.Error("Save succeeded with no config directory")
	}
	if err := Remove("work"); err == nil {
		t.Error("Remove succeeded with no config directory")
	}
}

// TestResolveClientSecretPath is the chain that used to be written out
// at four call sites, two of which trimmed whitespace and one of which
// did not — so a stored path of " " was skipped at login and used when
// serving.
func TestResolveClientSecretPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	def, err := DefaultClientSecretPath("work")
	if err != nil {
		t.Fatal(err)
	}

	got, err := ResolveClientSecretPath("work")
	if err != nil || got != def {
		t.Errorf("with nothing stored = %q, %v; want the default location", got, err)
	}

	if err := Save("work", Config{ClientSecretPath: "/stored/secret.json"}); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveClientSecretPath("work")
	if err != nil || got != "/stored/secret.json" {
		t.Errorf("stored path = %q, %v", got, err)
	}

	// An override wins, and the first non-blank one wins over the rest.
	got, err = ResolveClientSecretPath("work", "", "  ", "/flag/secret.json", "/later.json")
	if err != nil || got != "/flag/secret.json" {
		t.Errorf("override = %q, %v", got, err)
	}

	// Whitespace is not a path, wherever it comes from.
	if err := Save("work", Config{ClientSecretPath: "   "}); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveClientSecretPath("work", " ")
	if err != nil || got != def {
		t.Errorf("a blank stored path = %q, %v; want the default location", got, err)
	}
}

// TestProfilesAndSharingClient backs the warning `logout` gives.
//
// Profiles keep separate storage and share a *grant*: Google revokes the
// grant rather than the token, so a logout under one profile signs the
// account out of every profile using the same OAuth client. Measured
// live — a logout on a scratch profile silently signed out the default
// one — so `logout` has to be able to name them.
func TestProfilesAndSharingClient(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	if got, err := Profiles(); err != nil || len(got) != 0 {
		t.Fatalf("Profiles() with nothing configured = %v, %v", got, err)
	}

	for name, secret := range map[string]string{
		DefaultProfile: "/secrets/one.json",
		"work":         "/secrets/one.json",
		"personal":     "/secrets/two.json",
	} {
		if err := Save(name, Config{ClientSecretPath: secret}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"default", "personal", "work"}
	if !slices.Equal(got, want) {
		t.Errorf("Profiles() = %v, want %v", got, want)
	}

	// The two sharing a client secret are the two a revocation reaches.
	shared, err := SharingClient(DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0] != "work" {
		t.Errorf("SharingClient(default) = %v, want [work]", shared)
	}
	if shared, err := SharingClient("personal"); err != nil || len(shared) != 0 {
		t.Errorf("SharingClient(personal) = %v, %v; it uses its own client", shared, err)
	}

	// A profile with no client recorded cannot say who it shares with,
	// and must not guess that it shares with everybody.
	if err := Save("bare", Config{}); err != nil {
		t.Fatal(err)
	}
	if shared, err := SharingClient("bare"); err != nil || len(shared) != 0 {
		t.Errorf("SharingClient with no client secret = %v, %v", shared, err)
	}
}
