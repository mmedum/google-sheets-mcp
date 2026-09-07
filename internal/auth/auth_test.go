package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestScopes(t *testing.T) {
	full := Scopes(false)
	if len(full) != 2 || full[0] != ScopeSpreadsheets || full[1] != ScopeDriveReadonly {
		t.Errorf("full scopes are %v", full)
	}
	ro := Scopes(true)
	if len(ro) != 2 || ro[0] != ScopeSpreadsheetsReadonly {
		t.Errorf("read-only scopes are %v", ro)
	}
	// drive.file reaches only files the app created or the person picked
	// through a Picker a stdio server cannot show; full drive would let
	// this server manage files, which is another server's job.
	for _, s := range append(full, ro...) {
		if strings.HasSuffix(s, "/drive") || strings.HasSuffix(s, "/drive.file") {
			t.Errorf("scope %q is not one of ours", s)
		}
	}
}

func TestParseClientSecret(t *testing.T) {
	installed := `{"installed":{"client_id":"cid","client_secret":"sec","auth_uri":"https://auth.example.test/a","token_uri":"https://auth.example.test/t"}}`
	cfg, err := ParseClientSecret([]byte(installed), Scopes(false))
	if err != nil {
		t.Fatalf("ParseClientSecret: %v", err)
	}
	if cfg.ClientID != "cid" || cfg.Endpoint.AuthURL != "https://auth.example.test/a" {
		t.Errorf("parsed %+v", cfg)
	}

	// Google's own Sheets MCP needs a Web application client, so somebody
	// following its instructions arrives here with the wrong file. The
	// error says which kind to make.
	_, err = ParseClientSecret([]byte(`{"web":{"client_id":"cid"}}`), nil)
	if !errors.Is(err, ErrNotDesktopClient) || !strings.Contains(err.Error(), "Desktop") {
		t.Errorf("a web client gave %v", err)
	}
	if _, err := ParseClientSecret([]byte(`{}`), nil); !errors.Is(err, ErrNotDesktopClient) {
		t.Errorf("an empty client gave %v", err)
	}
	if _, err := ParseClientSecret([]byte(`not json`), nil); err == nil {
		t.Error("broken JSON was accepted")
	}
	if _, err := ParseClientSecret([]byte(`{"installed":{}}`), nil); err == nil {
		t.Error("a client with no client_id was accepted")
	}
	if _, err := LoadClientSecret("/no/such/file.json", nil); err == nil {
		t.Error("a missing file was accepted")
	}
	// The endpoints default to Google's when the file omits them.
	cfg, err = ParseClientSecret([]byte(`{"installed":{"client_id":"cid"}}`), nil)
	if err != nil || cfg.Endpoint.TokenURL != GoogleTokenURL {
		t.Errorf("defaults not applied: %+v, %v", cfg, err)
	}
}

// TestLoginRoundTrip drives the loopback flow end to end against a fake
// token endpoint, with the browser replaced by an HTTP call.
func TestLoginRoundTrip(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("code_verifier") == "" {
			t.Error("PKCE verifier missing from the exchange")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{
		ClientID: "cid",
		Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example.test/a", TokenURL: tokenSrv.URL, AuthStyle: oauth2.AuthStyleInParams},
	}

	tok, err := Login(context.Background(), cfg, LoginOptions{
		Listener: ln,
		OpenBrowser: func(raw string) error {
			u, err := url.Parse(raw)
			if err != nil {
				return err
			}
			q := u.Query()
			if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
				t.Errorf("no PKCE challenge in %s", raw)
			}
			if q.Get("access_type") != "offline" {
				t.Error("without access_type=offline Google returns no refresh token")
			}
			go func() {
				cb := q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&code=the-code"
				resp, err := http.Get(cb) //nolint:noctx // a test's own loopback call
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.RefreshToken != "rt" {
		t.Errorf("token = %+v", tok)
	}
}

func TestLoginRefusesAMismatchedState(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example.test/a", TokenURL: "https://auth.example.test/t"}}
	_, err = Login(context.Background(), cfg, LoginOptions{
		Listener: ln,
		OpenBrowser: func(raw string) error {
			u, _ := url.Parse(raw)
			go func() {
				resp, err := http.Get(u.Query().Get("redirect_uri") + "?state=wrong&code=c") //nolint:noctx // a test's own loopback call
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("Login accepted a mismatched state: %v", err)
	}
}

func TestLoginReportsAnAuthorizationDenial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example.test/a", TokenURL: "https://auth.example.test/t"}}
	_, err = Login(context.Background(), cfg, LoginOptions{
		Listener: ln,
		OpenBrowser: func(raw string) error {
			u, _ := url.Parse(raw)
			q := u.Query()
			go func() {
				resp, err := http.Get(q.Get("redirect_uri") + "?state=" + url.QueryEscape(q.Get("state")) + "&error=access_denied") //nolint:noctx // a test's own loopback call
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("a denial gave %v", err)
	}
}

func TestLoginTimesOutWaitingForTheBrowser(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{AuthURL: "https://auth.example.test/a"}}
	_, err = Login(context.Background(), cfg, LoginOptions{
		Listener:    ln,
		Timeout:     20 * time.Millisecond,
		OpenBrowser: func(string) error { return errors.New("no browser here") },
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Login = %v, want a timeout", err)
	}
}

// TestTokenRefreshIsBounded hangs a listener that accepts and never
// answers. Without a bounded client in the context the oauth2 refresh
// runs on http.DefaultClient, which has no timeout, and the first tool
// call hangs for as long as the process lives.
func TestTokenRefreshIsBounded(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold it open and say nothing at all.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "http://" + ln.Addr().String() + "/token"}}
	ts := TokenSource(context.Background(), cfg, "rt", 100*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := ts.Token()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent token endpoint returned a token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never came back; the token source is running on a client with no timeout")
	}
}

func TestInspectAndRevoke(t *testing.T) {
	var revoked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tokeninfo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"scope":"` + ScopeSpreadsheets + ` ` + ScopeDriveReadonly + `","email":"someone@example.test","expires_in":"3599","aud":"cid"}`))
		case "/revoke":
			_ = r.ParseForm()
			revoked = r.Form.Get("token")
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer srv.Close()

	oldInfo, oldRevoke := TokenInfoURL, RevokeURL
	TokenInfoURL, RevokeURL = srv.URL+"/tokeninfo", srv.URL+"/revoke"
	t.Cleanup(func() { TokenInfoURL, RevokeURL = oldInfo, oldRevoke })

	info, err := Inspect(context.Background(), srv.Client(), "at")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Email != "someone@example.test" || len(info.Scopes) != 2 || info.ExpiresIn != 3599*time.Second {
		t.Errorf("Inspect gave %+v", info)
	}
	if err := Revoke(context.Background(), srv.Client(), "rt"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked != "rt" {
		t.Errorf("Revoke sent %q", revoked)
	}

	TokenInfoURL, RevokeURL = srv.URL+"/nope", srv.URL+"/nope"
	if _, err := Inspect(context.Background(), srv.Client(), "at"); err == nil {
		t.Error("a non-200 tokeninfo was accepted")
	}
	if err := Revoke(context.Background(), srv.Client(), "rt"); err == nil {
		t.Error("a non-200 revoke was accepted")
	}
}

func TestMissingScopes(t *testing.T) {
	granted := []string{ScopeSpreadsheetsReadonly, ScopeDriveReadonly}
	missing := MissingScopes(granted, Scopes(false))
	if len(missing) != 1 || missing[0] != ScopeSpreadsheets {
		t.Errorf("MissingScopes = %v", missing)
	}
	if got := MissingScopes(granted, Scopes(true)); len(got) != 0 {
		t.Errorf("MissingScopes on a satisfied set = %v", got)
	}
}
