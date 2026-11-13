package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEncryptedCredentialsSurviveRestartAndRejectTampering(t *testing.T) {
	dir := t.TempDir()
	v, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	const token = "a-private-token-never-stored-raw"
	encrypted := v.Seal(token, "plex:one:token")
	if bytes.Contains(encrypted, []byte(token)) {
		t.Fatal("plaintext stored")
	}
	if bytes.Equal(encrypted, v.Seal(token, "plex:one:token")) {
		t.Fatal("nonce reused")
	}
	v, err = Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := v.Open(encrypted, "plex:one:token")
	if err != nil || plain != token {
		t.Fatal("restart lost token", err)
	}
	if _, err = v.Open(encrypted, "plex:other:token"); err == nil {
		t.Fatal("ciphertext could be swapped to another connection")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err = v.Open(encrypted, "plex:one:token"); err == nil {
		t.Fatal("tampering accepted")
	}
	info, err := os.Stat(filepath.Join(dir, "credentials.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions", err)
	}
}
func TestMissingCorruptAndExposedKeysFailClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, true); err == nil {
		t.Fatal("missing key replaced")
	}
	key := filepath.Join(dir, "credentials.key")
	if err := os.WriteFile(key, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, false); err == nil {
		t.Fatal("corrupt key replaced")
	}
	data, _ := os.ReadFile(key)
	if string(data) != "short" {
		t.Fatal("existing key overwritten")
	}
	if err := os.WriteFile(key, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, true); err == nil {
		t.Fatal("world-readable key accepted")
	}
}
func TestConcurrentKeyCreation(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	vaults := make(chan *Vault, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := Open(dir, false)
			if err != nil {
				errs <- err
			} else {
				vaults <- v
			}
		}()
	}
	wg.Wait()
	close(vaults)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var encrypted []byte
	for v := range vaults {
		if encrypted == nil {
			encrypted = v.Seal("secret", "same")
		} else if token, err := v.Open(encrypted, "same"); err != nil || token != "secret" {
			t.Fatal("different keys used during first run", err)
		}
	}
}
