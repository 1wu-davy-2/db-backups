package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	credentialVersion = byte(1)
	credentialAAD     = "dbbackups:credential:v1"
)

var ErrKeyMissing = errors.New("master key missing")

// Keyring 持有 32 字节主密钥。主密钥只驻留内存，不落日志、不进数据库。
type Keyring struct {
	master []byte
}

// LoadKeyring 优先读环境变量（便于 KMS/Secret 注入），否则读文件；文件不存在时生成。
func LoadKeyring(envValue, path string) (*Keyring, error) {
	if envValue != "" {
		raw, err := base64.StdEncoding.DecodeString(envValue)
		if err != nil {
			return nil, fmt.Errorf("BACKUP_MASTER_KEY must be base64: %w", err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("BACKUP_MASTER_KEY must decode to 32 bytes, got %d", len(raw))
		}
		return &Keyring{master: raw}, nil
	}

	raw, err := os.ReadFile(path)
	if err == nil {
		raw = bytes.TrimSpace(raw)
		if len(raw) != 32 {
			return nil, fmt.Errorf("master key file %s must be 32 bytes, got %d", path, len(raw))
		}
		return &Keyring{master: raw}, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read master key: %w", err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return &Keyring{master: key}, nil
}

func (k *Keyring) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.master)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt 加密数据库凭据。输出 1 字节版本 + 12 字节 nonce + 密文。
func (k *Keyring) Encrypt(plain string) ([]byte, error) {
	gcm, err := k.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, credentialVersion)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, []byte(plain), []byte(credentialAAD))
	return out, nil
}

func (k *Keyring) Decrypt(enc []byte) (string, error) {
	if len(enc) < 2 {
		return "", errors.New("ciphertext too short")
	}
	if enc[0] != credentialVersion {
		return "", fmt.Errorf("unsupported credential version %d", enc[0])
	}
	gcm, err := k.aead()
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(enc) < 1+ns {
		return "", errors.New("ciphertext too short")
	}
	nonce := enc[1 : 1+ns]
	plain, err := gcm.Open(nil, nonce, enc[1+ns:], []byte(credentialAAD))
	if err != nil {
		return "", errors.New("解密失败：主密钥与密文不匹配")
	}
	return string(plain), nil
}

// derive 用 HMAC-SHA256 做 KDF（等价于 HKDF-Extract 单轮），为每个文件派生独立密钥。
func (k *Keyring) derive(salt []byte, info string) []byte {
	m := hmac.New(sha256.New, k.master)
	m.Write(salt)
	m.Write([]byte(info))
	return m.Sum(nil)
}
