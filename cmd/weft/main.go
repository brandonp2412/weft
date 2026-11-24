package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
	"weft/internal/config"
	"weft/internal/matrix"
	"weft/internal/store"
	"weft/internal/tui"
)

var version = "0.1.0"

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "weft:", e)
		os.Exit(1)
	}
}
func run() error {
	flags := flag.NewFlagSet("weft", flag.ContinueOnError)
	dir := flags.String("data-dir", config.Dir(), "session and database directory (or WEFT_HOME)")
	settingsPath := flags.String("config", config.SettingsPath(), "TOML preferences file")
	demo := flags.Bool("demo", false, "explore the interface offline with sample conversations")
	showVersion := flags.Bool("version", false, "print version")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), `weft — a Matrix client for the terminal

Usage:
  weft [--data-dir PATH]                 Open your saved session
  weft [--data-dir PATH] login [options] Sign in with password, OAuth, SSO, or token
  weft [--data-dir PATH] logout          Revoke this session and archive local data
  weft --demo                           Explore the interface offline
  weft --version

Login options: --homeserver URL --user @name:server --oauth --sso --token
Passwords, tokens, and recovery secrets are prompted without echo.
Press F1 or type /help inside the client for commands.

`)
		flags.PrintDefaults()
	}
	if e := flags.Parse(os.Args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if *showVersion {
		fmt.Println("weft", version)
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	args := flags.Args()
	if len(args) > 0 {
		switch args[0] {
		case "login":
			return login(ctx, *dir, args[1:])
		case "logout":
			if len(args) != 1 {
				return errors.New("usage: weft logout")
			}
			return logout(ctx, *dir)
		case "help":
			flags.Usage()
			return nil
		default:
			return fmt.Errorf("unknown command %q (see weft --help)", args[0])
		}
	}
	settings, err := config.LoadSettings(*settingsPath)
	if err != nil {
		return err
	}
	if *demo {
		temp, e := os.MkdirTemp("", "weft-demo-*")
		if e != nil {
			return e
		}
		defer os.RemoveAll(temp)
		db, e := store.Open(temp)
		if e != nil {
			return e
		}
		defer db.Close()
		if e = tui.SeedDemo(db); e != nil {
			return e
		}
		_, e = tui.Run(ctx, tui.NewWithSettings(ctx, nil, db, true, settings))
		if errors.Is(e, tea.ErrProgramKilled) && ctx.Err() != nil {
			return nil
		}
		return e
	}
	c, e := config.Load(*dir)
	if os.IsNotExist(e) {
		fmt.Println("Welcome to weft.\n\nSign in:  weft login --user @you:server\nExplore:  weft --demo\n\nUse weft --help for more options.")
		return nil
	}
	if e != nil {
		return e
	}
	if e = config.Prepare(*dir); e != nil {
		return e
	}
	unlock, e := lock(*dir)
	if e != nil {
		return e
	}
	defer unlock()
	backend, e := matrix.Open(ctx, *dir, c)
	if e != nil {
		return e
	}
	defer backend.Close()
	backend.Start()
	_, e = tui.Run(ctx, tui.NewWithSettings(ctx, backend, backend.Store, false, settings))
	if errors.Is(e, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return e
}
func login(ctx context.Context, dir string, args []string) error {
	f := flag.NewFlagSet("weft login", flag.ContinueOnError)
	server := f.String("homeserver", "", "homeserver URL; discovered from the user ID if omitted")
	user := f.String("user", "", "Matrix user ID or localpart")
	sso := f.Bool("sso", false, "sign in through a browser (SSO)")
	token := f.Bool("token", false, "prompt for a device access token")
	oauthLogin := f.Bool("oauth", false, "sign in using native OAuth with PKCE")
	oauthClientID := f.String("oauth-client-id", "", "use a pre-registered native OAuth client")
	clientURI := f.String("client-uri", "", "published app URL for OAuth registration")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if len(f.Args()) != 0 {
		return errors.New("unexpected login arguments")
	}
	if (*sso && *token) || (*oauthLogin && (*sso || *token)) {
		return errors.New("choose one of --oauth, --sso, or --token")
	}
	if _, e := os.Stat(filepath.Join(dir, "session.json")); e == nil {
		return errors.New("already signed in; use a separate --data-dir for another account, or weft logout")
	}
	if e := config.Prepare(dir); e != nil {
		return e
	}
	unlock, e := lock(dir)
	if e != nil {
		return e
	}
	defer unlock()
	reader := bufio.NewReader(os.Stdin)
	if *user == "" && !*token {
		fmt.Print("Matrix ID (@you:server): ")
		*user, e = readLine(reader)
		if e != nil {
			return e
		}
	}
	if *server == "" && *token {
		fmt.Print("Homeserver URL: ")
		*server, e = readLine(reader)
		if e != nil {
			return e
		}
	}
	password, accessToken := "", ""
	if *token {
		accessToken, e = secret("Device access token: ")
	} else if !*sso && !*oauthLogin {
		password, e = secret("Password: ")
	}
	if e != nil {
		return e
	}
	authCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	showURL := func(url string) {
		fmt.Println("Open this URL in your browser to sign in:\n\n" + url + "\n\nWaiting for the browser callback…")
	}
	var c *config.Config
	if *oauthLogin {
		c, e = matrix.LoginOAuth(authCtx, *server, *user, *oauthClientID, *clientURI, showURL)
	} else {
		c, e = matrix.Login(authCtx, *server, *user, password, accessToken, *sso, showURL)
	}
	if e != nil {
		return e
	}
	// Save credentials before crypto initialization so a transient network failure
	// cannot strand an unrecorded device. Initialization is retried on next launch.
	if e = config.Save(dir, c); e != nil {
		return e
	}
	b, e := matrix.Open(ctx, dir, c)
	if e != nil {
		return fmt.Errorf("session saved, but encryption setup failed (retry by running weft): %w", e)
	}
	if e = b.Close(); e != nil {
		return e
	}
	fmt.Printf("Signed in as %s · device %s\nRun weft to open your conversations. Use /verify or /recover to verify this device.\n", c.UserID, c.DeviceID)
	return nil
}
func logout(ctx context.Context, dir string) error {
	c, e := config.Load(dir)
	if e != nil {
		return e
	}
	unlock, e := lock(dir)
	if e != nil {
		return e
	}
	defer unlock()
	cli, e := matrix.NewSessionClient(dir, c)
	if e != nil {
		return e
	}
	logoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if c.OAuthClientID != "" {
		e = cli.OAuthRevokeToken(logoutCtx)
	} else {
		_, e = cli.Logout(logoutCtx)
	}
	if e != nil {
		return fmt.Errorf("could not revoke the session; local data retained: %w", e)
	}
	c.AccessToken = ""
	c.RefreshToken = ""
	if e = config.Save(dir, c); e != nil {
		return e
	}
	archive := dir + ".logged-out-" + time.Now().Format("20060102-150405")
	if e = os.Rename(dir, archive); e != nil {
		return fmt.Errorf("session revoked, but could not archive local data: %w", e)
	}
	fmt.Println("Signed out. Local history and encryption data archived at", archive)
	return nil
}
func readLine(r *bufio.Reader) (string, error) {
	s, e := r.ReadString('\n')
	if e != nil {
		return "", e
	}
	return strings.TrimSpace(s), nil
}
func secret(label string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("secret input requires a terminal")
	}
	fmt.Print(label)
	b, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(b), e
}
