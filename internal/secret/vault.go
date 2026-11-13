// Package secret protects application credentials at rest. The encryption key is
// stored separately from SQLite, but in the same protected persistent directory.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Vault struct{ aead cipher.AEAD }

// Open never replaces an invalid key or creates a replacement when encrypted
// credentials already exist. Atomic hard-link publication handles first-run races.
func Open(dir string, requireExisting bool) (*Vault, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create credential key directory")
	}
	path := filepath.Join(dir, "credentials.key")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if requireExisting {
			return nil, errors.New("credential key is missing; restore credentials.key with the database backup")
		}
		temp, err := os.CreateTemp(dir, ".credentials-key-*")
		if err != nil {
			return nil, errors.New("cannot create credential key")
		}
		defer os.Remove(temp.Name())
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			temp.Close()
			return nil, err
		}
		if _, err = temp.Write(key); err != nil {
			temp.Close()
			return nil, errors.New("cannot write credential key")
		}
		if err = temp.Sync(); err != nil {
			temp.Close()
			return nil, errors.New("cannot sync credential key")
		}
		if err = temp.Close(); err != nil {
			return nil, err
		}
		if err = os.Link(temp.Name(), path); err != nil && !os.IsExist(err) {
			return nil, errors.New("cannot publish credential key")
		}
		d, err := os.Open(dir)
		if err != nil {
			return nil, err
		}
		err = d.Sync()
		d.Close()
		if err != nil {
			return nil, errors.New("cannot sync credential key directory")
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("cannot read credential key")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credentials.key must be a regular file accessible only to its owner (mode 0600)")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open credential key")
	}
	defer f.Close()
	key, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil || len(key) != 32 {
		return nil, errors.New("credentials.key is invalid; restore the original key rather than replacing it")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead}, nil
}
func (v *Vault) Seal(value, binding string) []byte {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	result := append([]byte{1}, nonce...)
	return v.aead.Seal(result, nonce, []byte(value), []byte(binding))
}
func (v *Vault) Open(value []byte, binding string) (string, error) {
	n := v.aead.NonceSize()
	if len(value) < 1+n+v.aead.Overhead() || value[0] != 1 {
		return "", errors.New("encrypted credential is invalid")
	}
	plain, err := v.aead.Open(nil, value[1:1+n], value[1+n:], []byte(binding))
	if err != nil {
		return "", errors.New("cannot decrypt credential; restore the original key and database")
	}
	return string(plain), nil
}
