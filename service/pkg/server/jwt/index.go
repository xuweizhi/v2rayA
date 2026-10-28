package jwt

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/v2rayA/v2rayA/db"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"sync"
)

var secret []byte
var once sync.Once

func genSecret() {
	var err error
	secret, err = loadSigningSecret(db.GetDB())
	if err != nil {
		log.Fatal("failed to load JWT signing secret: %v", err)
	}
}

// Persist random key material so tokens survive restarts without deriving the
// signing key from publicly knowable node hostnames or subscription URLs.
func loadSigningSecret(database *sql.DB) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if _, err := database.Exec("INSERT INTO system_config (key, value) VALUES ('auth:jwt-secret', ?) ON CONFLICT(key) DO NOTHING", hex.EncodeToString(key)); err != nil {
		return nil, err
	}
	var encoded string
	if err := database.QueryRow("SELECT value FROM system_config WHERE key = 'auth:jwt-secret'").Scan(&encoded); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("invalid stored JWT signing secret")
	}
	return key, nil
}
func getSecret() []byte {
	once.Do(genSecret)
	return secret
}
