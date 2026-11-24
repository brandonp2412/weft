package matrix

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
	"weft/internal/config"
)

func Homeserver(ctx context.Context, server, user string) (string, error) {
	if server == "" {
		u := id.UserID(user)
		_, domain, e := u.Parse()
		if e != nil {
			return "", errors.New("provide --homeserver or a full @user:server Matrix ID")
		}
		well, e := mautrix.DiscoverClientAPI(ctx, domain)
		if e == nil && well.Homeserver.BaseURL != "" {
			server = well.Homeserver.BaseURL
		} else {
			server = "https://" + domain
		}
	}
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, e := url.Parse(server)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("homeserver must be an http(s) URL")
	}
	return strings.TrimRight(server, "/"), nil
}
func Login(ctx context.Context, server, user, password, token string, sso bool, showURL func(string)) (*config.Config, error) {
	hs, e := Homeserver(ctx, server, user)
	if e != nil {
		return nil, e
	}
	cli, e := mautrix.NewClient(hs, "", "")
	if e != nil {
		return nil, e
	}
	cli.UserAgent = "weft/0.1.0"
	var resp *mautrix.RespLogin
	if token != "" {
		cli.AccessToken = token
		who, e := cli.Whoami(ctx)
		if e != nil {
			return nil, e
		}
		if who.DeviceID == "" {
			return nil, errors.New("access token must belong to a Matrix device")
		}
		return &config.Config{Homeserver: hs, UserID: who.UserID.String(), DeviceID: who.DeviceID.String(), AccessToken: token, PickleKey: config.NewKey()}, nil
	}
	req := &mautrix.ReqLogin{InitialDeviceDisplayName: "weft", StoreCredentials: true}
	if sso {
		loginToken, e := ssoToken(ctx, hs, showURL)
		if e != nil {
			return nil, e
		}
		req.Type = mautrix.AuthTypeToken
		req.Token = loginToken
	} else {
		req.Type = mautrix.AuthTypePassword
		req.Identifier = mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: user}
		req.Password = password
	}
	resp, e = cli.Login(ctx, req)
	if e != nil {
		return nil, e
	}
	return &config.Config{Homeserver: hs, UserID: resp.UserID.String(), DeviceID: resp.DeviceID.String(), AccessToken: resp.AccessToken, PickleKey: config.NewKey()}, nil
}
func ssoToken(ctx context.Context, hs string, show func(string)) (string, error) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", e
	}
	path := "/callback/" + url.PathEscape(config.NewKey())
	callback := "http://" + listener.Addr().String() + path
	tokens := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		token := r.URL.Query().Get("loginToken")
		if token == "" {
			http.Error(w, "Missing login token", 400)
			return
		}
		select {
		case tokens <- token:
			fmt.Fprintln(w, "Signed in. Return to weft.")
		default:
			http.Error(w, "Already received", 409)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go func() { _ = srv.Serve(listener) }()
	show(hs + "/_matrix/client/v3/login/sso/redirect?redirectUrl=" + url.QueryEscape(callback))
	select {
	case token := <-tokens:
		return token, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func NewSessionClient(dir string, c *config.Config) (*mautrix.Client, error) {
	cli, e := mautrix.NewClient(c.Homeserver, id.UserID(c.UserID), c.AccessToken)
	if e == nil {
		cli.DeviceID = id.DeviceID(c.DeviceID)
		configureOAuth(cli, dir, c)
	}
	return cli, e
}
