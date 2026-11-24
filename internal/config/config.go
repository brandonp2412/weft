package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	OAuthClientID string    `json:"oauth_client_id,omitempty"`
	RefreshToken  string    `json:"refresh_token,omitempty"`
	TokenExpiry   time.Time `json:"token_expiry,omitempty"`
	Homeserver    string    `json:"homeserver"`
	UserID        string    `json:"user_id"`
	DeviceID      string    `json:"device_id"`
	AccessToken   string    `json:"access_token"`
	PickleKey     string    `json:"pickle_key"`
}

func Dir() string {
	if p := os.Getenv("WEFT_HOME"); p != "" {
		return p
	}
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "weft")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "weft")
}
func Prepare(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}
func Load(dir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	if err = json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	if c.Homeserver == "" || c.UserID == "" || c.AccessToken == "" || c.DeviceID == "" || c.PickleKey == "" {
		return nil, errors.New("incomplete session; run weft login")
	}
	return &c, nil
}
func NewKey() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func Save(dir string, c *Config) error {
	if err := Prepare(dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(dir, "session.json"))
}
