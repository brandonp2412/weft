package matrix

import (
	"bytes"
	"encoding/base64"
	"testing"

	"maunium.net/go/mautrix/crypto/backup"
)

func TestDecodeStoredBackupKey(t *testing.T) {
	// Synthetic binary key starts with a non-base64 byte, reproducing the
	// production error without using any real account's recovery material.
	raw := bytes.Repeat([]byte{0xff}, 32)
	for name, data := range map[string][]byte{
		"standard":      raw,
		"legacy":        []byte(base64.RawStdEncoding.EncodeToString(raw)),
		"legacy padded": []byte(base64.StdEncoding.EncodeToString(raw)),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeStoredBackupKey(data)
			want, _ := backup.MegolmBackupKeyFromBytes(raw)
			if err != nil || !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Fatal("could not decode valid backup key", err)
			}
		})
	}
	for _, data := range [][]byte{nil, {1, 2}, bytes.Repeat([]byte{'?'}, 43), bytes.Repeat([]byte{'a'}, 64)} {
		if _, err := decodeStoredBackupKey(data); err == nil {
			t.Fatal("accepted invalid backup key")
		}
	}
}
