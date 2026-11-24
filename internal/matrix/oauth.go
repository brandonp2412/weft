package matrix

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/oauth"
	"weft/internal/config"
)

func LoginOAuth(ctx context.Context, server, user, clientID, clientURI string, showURL func(string)) (*config.Config, error) {
	hs, e := Homeserver(ctx, server, user)
	if e != nil {
		return nil, e
	}
	cli, e := mautrix.NewClient(hs, "", "")
	if e != nil {
		return nil, e
	}
	meta, e := cli.OAuthGetServerMetadata(ctx)
	if e != nil {
		return nil, fmt.Errorf("discover OAuth server (try password login or --sso for older servers): %w", e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/callback"
	if clientID == "" {
		// client_uri is optional in native OAuth registration; omit it until this
		// project has a published homepage, or accept a user-supplied URI.
		metadata := map[string]any{"application_type": "native", "client_name": "weft", "redirect_uris": []string{redirect}, "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}
		if clientURI != "" {
			metadata["client_uri"] = clientURI
		}
		var registered oauth.ClientMetadata
		_, e = cli.MakeRequest(ctx, http.MethodPost, meta.RegistrationEndpoint, metadata, &registered)
		if e != nil {
			return nil, fmt.Errorf("OAuth registration rejected; use --oauth-client-id with a pre-registered native client, or --client-uri with your published app URL: %w", e)
		}
		clientID = registered.ClientID
		if clientID == "" {
			return nil, errors.New("OAuth server returned no client ID")
		}
	}
	device := id.DeviceID("WEFT_" + config.NewKey()[:12])
	auth, e := cli.OAuthGetAuthorizationURL(ctx, oauth.GetAuthorizationURLParams{ClientID: clientID, RedirectURI: redirect, Scopes: oauth.ScopeList{oauth.ScopeOpenID, oauth.ScopeClientAPI, oauth.ScopeDevice(device)}, UserIDHint: id.UserID(user), ResponseMode: oauth.ResponseModeQuery})
	if e != nil {
		return nil, e
	}
	type response struct {
		code string
		err  error
	}
	responses := make(chan response, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(auth.State)) != 1 {
			http.Error(w, "Invalid login state", 400)
			return
		}
		if issuer := q.Get("iss"); issuer != "" && issuer != meta.Issuer {
			http.Error(w, "Invalid issuer", 400)
			return
		}
		var res response
		if q.Get("error") != "" {
			res.err = fmt.Errorf("OAuth authorization: %s", q.Get("error"))
		} else if res.code = q.Get("code"); res.code == "" {
			http.Error(w, "Missing authorization code", 400)
			return
		}
		select {
		case responses <- res:
			fmt.Fprintln(w, "Authorization received. Return to weft.")
		default:
			http.Error(w, "Already received", 409)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go func() { _ = srv.Serve(listener) }()
	showURL(auth.URL)
	var code string
	select {
	case response := <-responses:
		if response.err != nil {
			return nil, response.err
		}
		code = response.code
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	started := time.Now()
	tokens, e := cli.OAuthExchangeToken(ctx, oauth.ExchangeTokenParams{ClientID: clientID, CodeVerifier: auth.CodeVerifier, RedirectURI: redirect, Code: code})
	if e != nil {
		return nil, e
	}
	cli.AccessToken = tokens.AccessToken
	// Whoami establishes which user/device was actually authorized, rather than
	// trusting the optional login hint or the requested device scope.
	who, e := cli.Whoami(ctx)
	if e != nil {
		return nil, e
	}
	if who.DeviceID == "" {
		return nil, errors.New("OAuth token has no Matrix device scope")
	}
	return &config.Config{Homeserver: hs, UserID: who.UserID.String(), DeviceID: who.DeviceID.String(), AccessToken: tokens.AccessToken, PickleKey: config.NewKey(), OAuthClientID: clientID, RefreshToken: tokens.RefreshToken, TokenExpiry: started.Add(tokens.ExpiresIn.Duration)}, nil
}
func configureOAuth(cli *mautrix.Client, dir string, c *config.Config) {
	if c.OAuthClientID == "" {
		return
	}
	cli.OAuthSetTokens(c.OAuthClientID, c.RefreshToken, c.AccessToken, c.TokenExpiry)
	cli.SaveNewToken = func(_ context.Context, refresh, access string, expiry time.Time) error {
		updated := *c
		updated.RefreshToken = refresh
		updated.AccessToken = access
		updated.TokenExpiry = expiry
		if e := config.Save(dir, &updated); e != nil {
			return e
		}
		*c = updated
		return nil
	}
}
