package infini_setting

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

type InfiniSetting struct {
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}

var infiniSetting InfiniSetting
var snapshot config.Snapshot[InfiniSetting]

func init() {
	config.GlobalConfig.RegisterSnapshot("infini_setting", &infiniSetting, publishInfiniSetting)
}

func publishInfiniSetting() { snapshot.Publish(infiniSetting) }

func GetInfiniSetting() *InfiniSetting { return snapshot.Load() }

func ValidateKeyID(key string) error {
	if len(key) != 32 {
		return errors.New("infini key id must be 32 Base58 characters")
	}
	for _, ch := range key {
		if !strings.ContainsRune("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", ch) {
			return errors.New("infini key id must be 32 Base58 characters")
		}
	}
	return nil
}

func parsePrivateKey(value string) (ed25519.PrivateKey, error) {
	value = strings.TrimSpace(value)
	block, rest := pem.Decode([]byte(value))
	if !strings.HasPrefix(value, "-----BEGIN PRIVATE KEY-----") || block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("infini private key must be one unencrypted PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("infini private key is not valid PKCS#8")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("infini private key must use Ed25519")
	}
	return key, nil
}

func ValidatePrivateKey(value string) error {
	_, err := parsePrivateKey(value)
	return err
}

// ValidateOption validates credentials before the existing settings API saves them.
func ValidateOption(key, value string) error {
	switch key {
	case "infini_setting.key_id":
		if value == "" {
			return nil
		}
		return ValidateKeyID(value)
	case "infini_setting.private_key":
		return ValidatePrivateKey(value)
	default:
		return errors.New("unknown infini credential setting")
	}
}

func CredentialsConfigured() bool {
	s := GetInfiniSetting()
	return ValidateKeyID(s.KeyID) == nil && ValidatePrivateKey(s.PrivateKey) == nil
}

// SignRequest binds the exact path, query and body that the caller sends.
func SignRequest(req *http.Request, body []byte) error {
	s := GetInfiniSetting()
	if err := ValidateKeyID(s.KeyID); err != nil {
		return err
	}
	key, err := parsePrivateKey(s.PrivateKey)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(req.URL.EscapedPath(), "/v2/") {
		return errors.New("infini signing requires a v2 path")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return errors.New("infini nonce generation failed")
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	hash := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	req.Method = strings.ToUpper(req.Method)
	payload := strings.Join([]string{"INFINI-OPENAPI-V2", s.KeyID, req.Method, req.URL.RequestURI(), timestamp, nonce, digest}, "\n") + "\n"
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(payload)))
	req.Header.Del("Date")
	req.Header.Set("X-API-Key-ID", s.KeyID)
	req.Header.Set("X-API-Timestamp", timestamp)
	req.Header.Set("X-API-Nonce", nonce)
	req.Header.Set("Digest", digest)
	req.Header.Set("Authorization", `Ed25519 signature="`+signature+`"`)
	req.Header.Set("Content-Type", "application/json")
	return nil
}
