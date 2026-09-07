// Command google-sheets-mcp is a Model Context Protocol server for
// Google Sheets, plus the subcommands that set it up.
//
//	google-sheets-mcp            serve over stdio (what a client runs)
//	google-sheets-mcp login      authorise a Google account
//	google-sheets-mcp logout     revoke and forget the stored token
//	google-sheets-mcp status     what this profile has stored
//	google-sheets-mcp doctor     check the setup end to end
//
// Stdout carries JSON-RPC frames and nothing else. Every message a
// person reads goes to stderr, which is why the subcommands print there
// too: one rule is easier to keep than two.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/mmedum/google-sheets-mcp/internal/auth"
	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/credentials"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/redact"
	"github.com/mmedum/google-sheets-mcp/internal/server"
	"github.com/mmedum/google-sheets-mcp/internal/service"
	"github.com/mmedum/google-sheets-mcp/internal/tools"
	"github.com/mmedum/google-sheets-mcp/internal/userconfig"
	"github.com/mmedum/google-sheets-mcp/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "google-sheets-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet("google-sheets-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	settings := config.Define(fs, os.Getenv)
	showVersion := fs.Bool("version", false, "print the version and exit")
	dumpSchemas := fs.Bool("dump-schemas", false, "print the tool schemas as JSON and exit")
	clientSecret := fs.String("secret", "", "path to the OAuth client JSON, for login")
	spreadsheet := fs.String("spreadsheet", "", "a spreadsheet id, URL or title for doctor to read one cell of")
	yes := fs.Bool("yes", false, "do not ask for confirmation (logout)")
	localOnly := fs.Bool("local", false, "logout: delete this profile's stored token without revoking it at Google, so other profiles keep working")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version.Info())
		return nil
	}
	cfg, err := settings.Build()
	if err != nil {
		return err
	}
	log := config.NewLogger(cfg, stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "", "serve":
		if *dumpSchemas {
			return dump(ctx, cfg, log, stdout)
		}
		return serve(ctx, cfg, log, stderr)
	case "login":
		return login(ctx, cfg, *clientSecret, stderr)
	case "logout":
		return logout(ctx, cfg, *yes, *localOnly, stderr)
	case "status":
		return status(ctx, cfg, stderr)
	case "doctor":
		return doctor(ctx, cfg, *spreadsheet, stderr)
	}
	_, _ = fmt.Fprint(stderr, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

const usage = `google-sheets-mcp — a Model Context Protocol server for Google Sheets.

  google-sheets-mcp            serve over stdio (what an MCP client runs)
  google-sheets-mcp login      authorise a Google account
  google-sheets-mcp logout     revoke and forget the stored token
  google-sheets-mcp status     what this profile has stored
  google-sheets-mcp doctor     check the setup end to end

Settings come from GSHEETS_* environment variables, each with a flag of
the same name. See docs/configuration.md.

`

// build assembles the client and the service for one profile.
//
// It never fails on a missing credential. A server that exits because
// nobody has logged in shows the person "failed to connect" and the
// model never learns why; one that starts and answers every call with
// [auth] tells both of them exactly what to do.
func build(ctx context.Context, cfg config.Config, log *slog.Logger) (*service.Service, oauth2.TokenSource, error) {
	secretPath, err := userconfig.ResolveClientSecretPath(cfg.Profile, cfg.ClientSecretPath)
	if err != nil {
		return nil, nil, err
	}
	ts := &lazyTokenSource{cfg: cfg, log: log, secretPath: secretPath, ctx: ctx}
	client := gapi.New(ts, gapi.Options{
		Logger: log, ReadTimeout: cfg.HTTPTimeout, WriteTimeout: cfg.WriteTimeout,
		UserAgent: "google-sheets-mcp/" + version.String(),
	})
	return service.New(service.Deps{API: client, Config: cfg, Logger: log}), ts, nil
}

// lazyTokenSource keeps the credential lookup off the startup path.
//
// Reading the OS keyring is a synchronous call to a desktop service: a
// locked keyring prompts and a dead session bus times out, and doing it
// before the server reaches Run is exactly the "client reports a dead
// server" failure the warm goroutine exists to prevent. So the lookup
// happens once, on first use, which is either the warm goroutine or the
// first tool call — and either way the server is already serving.
type lazyTokenSource struct {
	cfg        config.Config
	log        *slog.Logger
	secretPath string
	ctx        context.Context

	once  sync.Once
	inner oauth2.TokenSource
}

func (l *lazyTokenSource) Token() (*oauth2.Token, error) {
	l.once.Do(func() {
		oauthCfg, err := auth.LoadClientSecret(l.secretPath, auth.Scopes(l.cfg.ReadOnly, l.cfg.EnableDataSources))
		if err != nil {
			l.inner = gapi.NoCredentials{Reason: err}
			return
		}
		store, err := tokenStore(l.cfg, l.log)
		if err != nil {
			l.inner = gapi.NoCredentials{Reason: err}
			return
		}
		token, source, err := store.Resolve()
		if err != nil {
			l.inner = gapi.NoCredentials{Reason: err}
			return
		}
		l.log.Debug("credentials resolved", "profile", l.cfg.Profile, "store", string(source))
		l.inner = auth.TokenSource(l.ctx, oauthCfg, token, l.cfg.HTTPTimeout)
	})
	return l.inner.Token()
}

// recordAccount fills in the address the profile carries.
//
// Every sibling server records one and this did not, which is the
// difference a `status` output shows and nobody can act on. It is not
// from the token: `tokeninfo` returns an address only when the token
// carries an email scope, and this server asks for neither `openid` nor
// `userinfo.email` (§17.6) — one of the siblings does, and adding a
// third scope to record a string would be the wrong way to match a
// convention. It comes from the Drive call this server already makes.
//
// Best effort. The token is saved by the time this runs, so a failure
// here costs a field in a config file and never a login.
func recordAccount(ctx context.Context, cfg config.Config, out io.Writer, uc *userconfig.Config) {
	svc, _, err := build(ctx, cfg, config.NewLogger(cfg, out))
	if err != nil {
		return
	}
	if addr, err := svc.Account(ctx); err == nil {
		uc.AccountEmail = addr
	}
}

func tokenStore(cfg config.Config, log *slog.Logger) (*credentials.Store, error) {
	path, err := userconfig.TokenFilePath(cfg.Profile)
	if err != nil {
		return nil, err
	}
	// The profile's own record of where the token went, so a keyring
	// that answers "nothing" can be told from a login that never
	// happened. Read here rather than in the credentials package, which
	// touches secrets and should not also grow a dependency on
	// non-secret state; a profile that cannot be read is simply no
	// second opinion.
	uc, _ := userconfig.Load(cfg.Profile)
	return &credentials.Store{
		Profile:       cfg.Profile,
		Keyring:       credentials.OSKeyring(),
		FilePath:      path,
		Warn:          func(m string) { log.Warn(m) },
		ExpectKeyring: uc.TokenStore == string(credentials.SourceKeyring),
	}, nil
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger, stderr io.Writer) error {
	svc, ts, err := build(ctx, cfg, log)
	if err != nil {
		return err
	}
	s := server.New(server.Deps{Service: svc, Config: cfg, Logger: log, Version: version.String()})

	// Warm the token off the startup path. A failure is logged and the
	// server still serves, so the model gets an actionable [auth] on the
	// first call instead of the client reporting a dead server.
	go func() {
		// The token source carries its own bounded client, so this
		// needs no deadline of its own — and waiting on one would hold
		// the goroutine and its timer for a minute after the work was
		// done.
		if _, err := ts.Token(); err != nil {
			log.Warn("no usable credentials; the server is running and every tool will say so",
				"profile", cfg.Profile, "class", gapi.Class(err))
			return
		}
		log.Debug("credentials warmed", "profile", cfg.Profile)
	}()

	log.Info("serving on stdio", "version", version.String(), "profile", cfg.Profile,
		"read_only", cfg.ReadOnly, "destructive", cfg.EnableDestructive)
	err = s.Run(ctx, &mcp.StdioTransport{})
	if cleanDisconnect(err) {
		log.Debug("client disconnected")
		return nil
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr)
	}
	return err
}

// cleanDisconnect reports whether err is an ordinary end of session.
//
// errors.Is(err, io.EOF) does not catch it: the SDK reports a closed
// connection as JSON-RPC -32004 with the EOF only as message text, so an
// unmatched error makes the process exit non-zero on a normal
// disconnect, which every host logs as a crash. The code is matched, not
// the text.
func cleanDisconnect(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	var je *jsonrpc.Error
	if errors.As(err, &je) {
		return je.Code == -32004 || je.Code == -32003
	}
	return false
}

func dump(ctx context.Context, cfg config.Config, log *slog.Logger, stdout io.Writer) error {
	// The dump must describe the whole surface, not the surface this
	// configuration happens to register, or a diff would swing with an
	// environment variable. Which flags that takes is the registration
	// layer's to say: listed here, the list went stale the first time a
	// gate was added.
	full := tools.FullSurface(cfg)
	svc := service.New(service.Deps{API: nil, Config: full, Logger: log})
	s := server.New(server.Deps{Service: svc, Config: full, Logger: log, Version: version.String()})
	return server.DumpSchemas(ctx, s, stdout, version.String())
}

func login(ctx context.Context, cfg config.Config, secretFlag string, out io.Writer) error {
	uc, err := userconfig.Load(cfg.Profile)
	if err != nil && !errors.Is(err, userconfig.ErrNotFound) {
		return err
	}
	secretPath, err := userconfig.ResolveClientSecretPath(cfg.Profile, secretFlag, cfg.ClientSecretPath)
	if err != nil {
		return err
	}
	scopes := auth.Scopes(cfg.ReadOnly, cfg.EnableDataSources)
	oauthCfg, err := auth.LoadClientSecret(secretPath, scopes)
	if err != nil {
		return fmt.Errorf("%w\n\nCreate a Desktop app OAuth client in your own Google Cloud project, download its JSON, "+
			"and pass it with -secret, or put it at %s", err, secretPath)
	}

	tok, err := auth.Login(ctx, oauthCfg, auth.LoginOptions{Out: out, HTTPTimeout: cfg.HTTPTimeout})
	if err != nil {
		return err
	}
	store, err := tokenStore(cfg, config.NewLogger(cfg, out))
	if err != nil {
		return err
	}
	source, err := store.Save(tok.RefreshToken)
	if err != nil {
		return err
	}

	uc.ClientSecretPath = secretPath
	uc.TokenStore = string(source)
	uc.Scopes = scopes
	recordAccount(ctx, cfg, out, &uc)
	if info, err := auth.Inspect(ctx, boundedClient(cfg), tok.AccessToken); err == nil {
		uc.AccountEmail = info.Email
		uc.Scopes = info.Scopes
		if missing := auth.MissingScopes(info.Scopes, scopes); len(missing) > 0 {
			_, _ = fmt.Fprintf(out, "\nWarning: these scopes were not granted: %s\n"+
				"Add them to the consent screen and run login again, or some tools will fail with [forbidden].\n",
				strings.Join(missing, ", "))
		}
	}
	if err := userconfig.Save(cfg.Profile, uc); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\nSigned in%s. Refresh token stored in the %s.\n", accountSuffix(uc.AccountEmail), source)
	return nil
}

// logout revokes the stored refresh token and forgets the account.
//
// Revocation is not per profile, and this is the one place that matters.
// Google revokes the *grant*, so a token revoked under one profile signs
// the account out of every profile sharing that OAuth client — measured
// live, where signing out of a scratch profile silently signed out the
// default one too. Profiles keep separate storage, not separate grants,
// and saying "signed out of profile X" while that happened would be a
// lie the person only discovers on their next call.
func logout(ctx context.Context, cfg config.Config, yes, localOnly bool, out io.Writer) error {
	store, err := tokenStore(cfg, config.NewLogger(cfg, out))
	if err != nil {
		return err
	}
	token, source, err := store.ResolveStored()
	if errors.Is(err, credentials.ErrNotFound) {
		_, _ = fmt.Fprintf(out, "Nothing stored for profile %q.\n", cfg.Profile)
		return nil
	}
	if err != nil {
		return err
	}
	shared, _ := userconfig.SharingClient(cfg.Profile)
	if !yes {
		_, _ = fmt.Fprintf(out, "This forgets the account for profile %q and deletes its refresh token from the %s.\n",
			cfg.Profile, source)
		if localOnly {
			_, _ = fmt.Fprintln(out, "With -local it is not revoked at Google, so other profiles and other machines keep working.")
		} else {
			_, _ = fmt.Fprintln(out, "It also revokes the token at Google, which revokes the whole grant:")
			_, _ = fmt.Fprintln(out, "every profile using the same OAuth client and account is signed out too.")
			if len(shared) > 0 {
				_, _ = fmt.Fprintf(out, "That is: %s. Use -local to delete this profile's copy without revoking.\n",
					strings.Join(shared, ", "))
			}
		}
		_, _ = fmt.Fprintln(out, "Pass -yes to go ahead.")
		return nil
	}
	if localOnly {
		_, _ = fmt.Fprintln(out, "Not revoking at Google (-local): other profiles and other machines keep working.")
	} else if err := auth.Revoke(ctx, boundedClient(cfg), token); err != nil {
		// A token Google has already forgotten is still one this machine
		// should stop holding.
		_, _ = fmt.Fprintf(out, "Revoking at Google failed (%v); deleting the local copy anyway.\n", err)
	}
	if err := store.Delete(); err != nil {
		return err
	}
	uc, err := userconfig.Load(cfg.Profile)
	if err == nil {
		uc.AccountEmail, uc.TokenStore, uc.Scopes = "", "", nil
		_ = userconfig.Save(cfg.Profile, uc)
	}
	_, _ = fmt.Fprintf(out, "Signed out of profile %q.\n", cfg.Profile)
	if !localOnly && len(shared) > 0 {
		_, _ = fmt.Fprintf(out, "The grant was revoked at Google, so %s %s signed out as well; each needs `login` again.\n",
			strings.Join(shared, ", "), plural(len(shared), "is", "are"))
	}
	return nil
}

// plural picks a verb for a list whose length varies.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func status(_ context.Context, cfg config.Config, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "%s\nprofile: %s\n", version.Info(), cfg.Profile)
	dir, err := userconfig.ProfileDir(cfg.Profile)
	if err == nil {
		_, _ = fmt.Fprintf(out, "config dir: %s\n", dir)
	}
	uc, err := userconfig.Load(cfg.Profile)
	switch {
	case errors.Is(err, userconfig.ErrNotFound):
		_, _ = fmt.Fprintln(out, "not configured: run `google-sheets-mcp login`")
	case err != nil:
		return err
	default:
		// Masked, because the issue form asks for this output and an
		// account address and an OAuth client id are both on the
		// never-list. The client id arrives through the path: the Cloud
		// console names the file after it.
		_, _ = fmt.Fprintf(out, "account: %s\nclient secret: %s\ntoken store: %s\nscopes: %s\n",
			orNone(redact.Email(uc.AccountEmail)), orNone(redact.Path(uc.ClientSecretPath)),
			orNone(uc.TokenStore), orNone(strings.Join(uc.Scopes, " ")))
	}
	_, _ = fmt.Fprintf(out, "read-only: %v\ndestructive tools: %v\n", cfg.ReadOnly, cfg.EnableDestructive)
	return nil
}

// errSkipped marks a check that could not run because an earlier one
// failed. It is not a failure of its own.
var errSkipped = errors.New("skipped")

// doctor checks the setup in the order it has to be set up, and names
// what is missing rather than reporting a failure. Most first-run
// reports are an API that was never enabled or a consent screen that
// never got the scopes.
func doctor(ctx context.Context, cfg config.Config, spreadsheet string, out io.Writer) error {
	_, _ = fmt.Fprintf(out, "%s\nprofile: %s\n\n", version.Info(), cfg.Profile)
	fail := 0
	step := func(name string, fn func() (string, error)) {
		detail, err := fn()
		switch {
		case errors.Is(err, errSkipped):
			// A check that could not run is not a check that failed.
			// Reporting it as a failure buries the one thing that is
			// actually wrong under three that are only consequences.
			_, _ = fmt.Fprintf(out, "  skip  %s  (%v)\n", name, err)
		case err != nil:
			fail++
			_, _ = fmt.Fprintf(out, "  FAIL  %s\n        %v\n", name, err)
		default:
			_, _ = fmt.Fprintf(out, "  ok    %s%s\n", name, detailSuffix(detail))
		}
	}

	var oauthCfg *oauth2.Config
	scopes := auth.Scopes(cfg.ReadOnly, cfg.EnableDataSources)

	step("OAuth client JSON", func() (string, error) {
		path, err := userconfig.ResolveClientSecretPath(cfg.Profile, cfg.ClientSecretPath)
		if err != nil {
			return "", err
		}
		oauthCfg, err = auth.LoadClientSecret(path, scopes)
		if err != nil {
			return "", fmt.Errorf("%s\n        create a Desktop app OAuth client in your own Cloud project and run `google-sheets-mcp login -secret <file>`",
				redact.Path(err.Error()))
		}
		return redact.Path(path), nil
	})

	var token *oauth2.Token
	step("refresh token", func() (string, error) {
		if oauthCfg == nil {
			return "", fmt.Errorf("%w: there is no OAuth client to exchange it with", errSkipped)
		}
		store, err := tokenStore(cfg, config.NewLogger(cfg, out))
		if err != nil {
			return "", err
		}
		refresh, source, err := store.Resolve()
		if err != nil {
			return "", err
		}
		token, err = auth.TokenSource(ctx, oauthCfg, refresh, cfg.HTTPTimeout).Token()
		if err != nil {
			return "", fmt.Errorf("%w\n        run `google-sheets-mcp login` again", err)
		}
		return "from the " + string(source), nil
	})

	step("granted scopes", func() (string, error) {
		if token == nil {
			return "", fmt.Errorf("%w: there is no token to inspect", errSkipped)
		}
		info, err := auth.Inspect(ctx, boundedClient(cfg), token.AccessToken)
		if err != nil {
			return "", err
		}
		if missing := auth.MissingScopes(info.Scopes, scopes); len(missing) > 0 {
			return "", fmt.Errorf("not granted: %s\n        add them to the consent screen, then run `google-sheets-mcp login` again",
				strings.Join(missing, ", "))
		}
		return strings.Join(info.Scopes, " "), nil
	})

	svc, _, err := build(ctx, cfg, config.NewLogger(cfg, out))
	if err != nil {
		return err
	}
	step("Drive API", func() (string, error) {
		if token == nil {
			return "", fmt.Errorf("%w: no credentials to call it with", errSkipped)
		}
		who, err := svc.Describe(ctx)
		if err != nil {
			return "", fmt.Errorf("%w\n        enable the Google Drive API in your Cloud project", err)
		}
		// Enough of the address for the person to recognise their own
		// account, and nothing for a reader of a pasted report.
		return who, nil
	})

	if spreadsheet == "" {
		_, _ = fmt.Fprintln(out, "  skip  Sheets API (pass -spreadsheet <id|url|title> to check it)")
	} else {
		// Nothing a step reports may name the spreadsheet. A count, a
		// shape or a class is a diagnostic; a title, a range with a
		// sheet in it or a cell's contents is the person's data, and the
		// issue form asks people to paste this output. A title has no
		// shape a redactor could catch — that is the whole difficulty —
		// so the rule has to be kept here, at the point of writing.
		var sheetTitle string
		step("Sheets API: get_spreadsheet", func() (string, error) {
			if token == nil {
				return "", fmt.Errorf("%w: no credentials to call it with", errSkipped)
			}
			card, err := svc.Card(ctx, spreadsheet)
			if err != nil {
				return "", fmt.Errorf("%w\n        enable the Google Sheets API in your Cloud project", err)
			}
			if len(card.Sheets) > 0 {
				sheetTitle = card.Sheets[0]
			}
			return fmt.Sprintf("%d sheet(s)", len(card.Sheets)), nil
		})
		step("Sheets API: read one cell", func() (string, error) {
			if sheetTitle == "" {
				return "", fmt.Errorf("%w: no sheet to read", errSkipped)
			}
			// The result is deliberately discarded. Reporting any of it
			// would put the sheet title or a cell's contents into
			// output the issue form asks people to paste, and a title
			// has no shape a redactor could catch. Removing the value
			// path beats sanitising it: there is then nothing here for
			// a later edit to reintroduce.
			if _, err := svc.Read(ctx, service.ReadRequest{
				Spreadsheet: spreadsheet, Sheet: sheetTitle, Range: "A1:A1",
				Budget: service.Budget{Cells: 1},
			}); err != nil {
				return "", err
			}
			return "A1 of the first sheet", nil
		})
	}

	if fail > 0 {
		return fmt.Errorf("%d check(s) failed", fail)
	}
	_, _ = fmt.Fprintln(out, "\nAll checks passed.")
	return nil
}

// boundedClient is the one used for the OAuth calls doctor and login
// make directly. The oauth2 library falls back to http.DefaultClient,
// which has no timeout at all.
func boundedClient(cfg config.Config) *http.Client {
	return &http.Client{Timeout: cfg.HTTPTimeout}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func accountSuffix(email string) string {
	if email == "" {
		return ""
	}
	return " as " + redact.Email(email)
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return "  (" + detail + ")"
}
