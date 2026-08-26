package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// DecryptSubscriptionBody decrypts the body of a password-protected
// subscription (the scheme announced by the "subscription-encryption: true"
// response header): the body is base64 encoded, its first 16 bytes are the
// AES-128-CBC IV, and the key is the MD5 digest of the subscription password.
func DecryptSubscriptionBody(body []byte, password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("subscription is encrypted, please provide the subscription password")
	}
	s := strings.TrimSpace(string(body))
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", ""), "\r", "")
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// some providers omit padding
		if raw2, err2 := base64.RawStdEncoding.DecodeString(s); err2 == nil {
			raw = raw2
			err = nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("subscription body is not valid base64: %w", err)
	}
	if len(raw) <= 16 {
		return "", fmt.Errorf("subscription body is too short to decrypt")
	}
	iv := raw[:16]
	ciphertext := raw[16:]
	key := md5.Sum([]byte(password)) // #nosec G401 -- scheme defined by the subscription-encryption convention
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("failed to init AES cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	// strip PKCS#7 padding
	if len(plaintext) == 0 {
		return "", fmt.Errorf("decrypted subscription body is empty")
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen <= 0 || padLen > aes.BlockSize || padLen > len(plaintext) {
		return "", fmt.Errorf("decrypted subscription body has invalid padding")
	}
	for _, b := range plaintext[len(plaintext)-padLen:] {
		if int(b) != padLen {
			return "", errors.New("decrypted subscription body has invalid padding")
		}
	}
	return string(plaintext[:len(plaintext)-padLen]), nil
}

// CryptoPwd 使用 bcrypt 加密密码。
// 如果 bcrypt 失败（极少情况），回退到旧版 MD5 哈希方式以保持兼容。
func CryptoPwd(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		// fallback to legacy hash on error
		shaed := sha512.Sum512_256([]byte(password))
		pwd := md5.Sum(shaed[:])
		return fmt.Sprintf("%x", pwd)
	}
	return string(hash)
}

// isBcryptHash 判断字符串是否为 bcrypt 哈希（以 $2a$、$2b$ 或 $2y$ 开头）
func isBcryptHash(hash string) bool {
	return len(hash) >= 4 && (hash[:4] == "$2a$" || hash[:4] == "$2b$" || hash[:4] == "$2y$")
}

// isLegacyMD5Hash 判断字符串是否为旧版 MD5 哈希（32位十六进制字符串）
func isLegacyMD5Hash(hash string) bool {
	if len(hash) != 32 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// CheckPassword 验证密码，优先使用 bcrypt，同时向后兼容旧版 MD5 哈希。
// 旧版哈希方式：sha512.Sum512_256(password) → md5.Sum → 32位十六进制字符串
func CheckPassword(password, hash string) bool {
	// 如果是 bcrypt 哈希，使用 bcrypt 验证
	if isBcryptHash(hash) {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	// 如果是旧版 MD5 哈希（32位十六进制），使用旧版方式验证
	if isLegacyMD5Hash(hash) {
		shaed := sha512.Sum512_256([]byte(password))
		pwd := md5.Sum(shaed[:])
		return fmt.Sprintf("%x", pwd) == hash
	}
	return false
}

// HMACSHA256
func HMACSHA256(s string, secret []byte) []byte {
	h := hmac.New(sha256.New, secret)
	io.WriteString(h, s)
	return h.Sum(nil)
}

// 封装base64.StdEncoding进行解码，加入了长度补全，换行删除。当error时，返回输入和err
func Base64StdDecode(s string) (string, error) {
	s = strings.TrimSpace(s)
	saver := s
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", ""), "\r", "")
	if len(s)%4 > 0 {
		s += strings.Repeat("=", 4-len(s)%4)
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return saver, err
	}
	return string(raw), err
}

// 封装base64.URLEncoding进行解码，加入了长度补全，换行删除。当error时，返回输入和err
func Base64URLDecode(s string) (string, error) {
	s = strings.TrimSpace(s)
	saver := s
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", ""), "\r", "")
	if len(s)%4 > 0 {
		s += strings.Repeat("=", 4-len(s)%4)
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return saver, err
	}
	return string(raw), err
}

// StringToUUID5 is from https://github.com/XTLS/Xray-core/issues/158
func StringToUUID5(str string) string {
	var Nil [16]byte
	h := sha1.New()
	h.Write(Nil[:])
	h.Write([]byte(str))
	u := h.Sum(nil)[:16]
	u[6] = (u[6] & 0x0f) | (5 << 4)
	u[8] = u[8]&(0xff>>2) | (0x02 << 6)
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], u[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], u[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], u[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], u[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:], u[10:])
	return string(buf)
}

func GetCertInfo(crt string) (names []string, err error) {
	b, err := os.ReadFile(crt)
	if err != nil {
		return nil, err
	}
	p, _ := pem.Decode(b)
	if p == nil {
		return nil, fmt.Errorf("bad certificate")
	}
	cert, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		return nil, fmt.Errorf("bad certificate: %w", err)
	}
	names = append(names, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) <= 0 {
		return nil, fmt.Errorf("bad certificate: no names found")
	}
	return names, nil
}
