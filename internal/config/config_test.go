package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivatePersistentSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	want := &Config{Homeserver: "https://example.test", UserID: "@alice:example.test", DeviceID: "DEVICE", AccessToken: "secret", PickleKey: NewKey()}
	if e := Save(dir, want); e != nil {
		t.Fatal(e)
	}
	got, e := Load(dir)
	if e != nil {
		t.Fatal(e)
	}
	if *got != *want {
		t.Fatal("session changed")
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "session.json"): 0600} {
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s has permissions %o", path, info.Mode().Perm())
		}
	}
}
