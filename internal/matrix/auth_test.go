package matrix

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"weft/internal/config"
)

func TestSSOCallbackAndTokenExchange(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/login" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body["type"] != "m.login.token" || body["token"] != "test-login-token" {
			t.Errorf("wrong token exchange: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "@alice:test", "device_id": "NEWDEVICE", "access_token": "access"})
	}))
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := Login(ctx, hs.URL, "@alice:test", "", "", true, func(signin string) {
		u, e := url.Parse(signin)
		if e != nil {
			t.Error(e)
			return
		}
		callback := u.Query().Get("redirectUrl")
		resp, e := http.Get(callback + "?loginToken=test-login-token")
		if e != nil {
			t.Error(e)
			return
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("callback status %d", resp.StatusCode)
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	if c.UserID != "@alice:test" || c.DeviceID != "NEWDEVICE" || c.AccessToken != "access" {
		t.Fatalf("incorrect session: %+v", c)
	}
}
func TestHomeserverValidation(t *testing.T) {
	for _, hs := range []string{"file:///etc/passwd", "ftp://example.test", "https://"} {
		if _, e := Homeserver(context.Background(), hs, ""); e == nil {
			t.Errorf("accepted %q", hs)
		}
	}
}

func TestNativeOAuthPKCERefreshAndRevocation(t *testing.T) {
	var hs *httptest.Server
	var challenge, redirect string
	var revoked bool
	hs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/_matrix/client/v1/auth_metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": hs.URL, "authorization_endpoint": hs.URL + "/authorize", "registration_endpoint": hs.URL + "/register", "token_endpoint": hs.URL + "/token", "revocation_endpoint": hs.URL + "/revoke", "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			var metadata map[string]any
			_ = json.NewDecoder(r.Body).Decode(&metadata)
			if metadata["application_type"] != "native" || metadata["token_endpoint_auth_method"] != "none" {
				t.Error("incorrect native registration")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "weft-native-test"})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" {
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
					t.Error("PKCE challenge mismatch")
				}
				if r.Form.Get("redirect_uri") != redirect || r.Form.Get("code") != "oauth-code" {
					t.Error("wrong code exchange")
				}
				fmt.Fprint(w, `{"access_token":"oauth-access","refresh_token":"oauth-refresh","expires_in":3600,"token_type":"Bearer"}`)
			} else {
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "oauth-refresh" {
					t.Error("wrong refresh grant")
				}
				fmt.Fprint(w, `{"access_token":"oauth-refreshed","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`)
			}
		case "/_matrix/client/v3/account/whoami":
			if h := r.Header.Get("Authorization"); h != "Bearer oauth-access" && h != "Bearer oauth-refreshed" {
				t.Error("wrong bearer token")
			}
			fmt.Fprint(w, `{"user_id":"@alice:oauth.test","device_id":"OAUTHDEVICE"}`)
		case "/revoke":
			_ = r.ParseForm()
			if r.Form.Get("token") != "rotated-refresh" {
				t.Error("did not revoke refresh token")
			}
			revoked = true
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hs.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, e := LoginOAuth(ctx, hs.URL, "@alice:oauth.test", "", "", func(authURL string) {
		u, _ := url.Parse(authURL)
		q := u.Query()
		challenge = q.Get("code_challenge")
		redirect = q.Get("redirect_uri")
		if q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "urn:matrix:client:device:WEFT_") {
			t.Error("missing PKCE/device scope")
		}
		bad, e := http.Get(redirect + "?code=oauth-code&state=wrong")
		if e != nil {
			t.Error(e)
			return
		}
		bad.Body.Close()
		if bad.StatusCode != 400 {
			t.Error("bad OAuth state was accepted")
		}
		resp, e := http.Get(redirect + "?" + url.Values{"code": {"oauth-code"}, "state": {q.Get("state")}, "iss": {hs.URL}}.Encode())
		if e != nil {
			t.Error(e)
			return
		}
		resp.Body.Close()
	})
	if e != nil {
		t.Fatal(e)
	}
	if c.OAuthClientID != "weft-native-test" || c.RefreshToken != "oauth-refresh" || c.DeviceID != "OAUTHDEVICE" {
		t.Fatalf("incorrect OAuth session: %+v", c)
	}
	dir := t.TempDir()
	c.TokenExpiry = time.Now().Add(-time.Minute)
	if e = config.Save(dir, c); e != nil {
		t.Fatal(e)
	}
	cli, e := NewSessionClient(dir, c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cli.Whoami(ctx); e != nil {
		t.Fatal(e)
	}
	saved, e := config.Load(dir)
	if e != nil {
		t.Fatal(e)
	}
	if saved.AccessToken != "oauth-refreshed" || saved.RefreshToken != "rotated-refresh" {
		t.Fatal("rotated tokens not persisted")
	}
	if e = cli.OAuthRevokeToken(ctx); e != nil {
		t.Fatal(e)
	}
	if !revoked {
		t.Fatal("revocation not called")
	}
}
