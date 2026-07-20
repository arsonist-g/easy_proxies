package monitor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// 凭证明文以 AES-256-GCM 密文形式存入 db（KeyCipher/TokenCipher）；
// 加密密钥来自 config.yaml 的 credential_key（服务端持有，前端永不接触）。
// 列表接口不返回明文（仅前缀）；点复制时 plain 接口用此密钥解密返回明文。

// encryptSecret 用 AES-256-GCM 加密明文，返回 base64(nonce || ciphertext)。
func encryptSecret(keyHex, plain string) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid credential key (need 32-byte hex)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// decryptSecret 解密 encryptSecret 产出的密文；空密文返回空串。
func decryptSecret(keyHex, ctB64 string) (string, error) {
	if ctB64 == "" {
		return "", nil
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return "", errors.New("invalid credential key (need 32-byte hex)")
	}
	ct, err := base64.StdEncoding.DecodeString(ctB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ct) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	ns := gcm.NonceSize()
	pt, err := gcm.Open(nil, ct[:ns], ct[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
