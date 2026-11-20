package common

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// The standard scrypt work factor (18) uses 256 MiB. Limit concurrent
// passphrase derivations when several remote subscriptions refresh together.
var subscriptionScryptMu sync.Mutex

type subscriptionScryptIdentity struct{ *age.ScryptIdentity }

func (i subscriptionScryptIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	if len(stanzas) != 1 || stanzas[0].Type != "scrypt" {
		return nil, age.ErrIncorrectIdentity
	}
	subscriptionScryptMu.Lock()
	defer subscriptionScryptMu.Unlock()
	return i.ScryptIdentity.Unwrap(stanzas)
}

// DecodeSubscriptionBody opens an age envelope or the legacy password-protected
// AES response before the subscription parser sees the content.
func DecodeSubscriptionBody(body []byte, encryption, aesPassword, ageCredential string) ([]byte, error) {
	encryption = strings.TrimSpace(encryption)
	armored := bytes.TrimLeft(body, " \t\r\n")
	if bytes.HasPrefix(armored, []byte(armor.Header)) {
		return decryptAgeSubscription(armor.NewReader(bytes.NewReader(armored)), ageCredential)
	}
	if bytes.HasPrefix(body, []byte("age-encryption.org/v1\n")) || strings.EqualFold(encryption, "age") {
		return decryptAgeSubscription(bytes.NewReader(body), ageCredential)
	}
	if strings.EqualFold(encryption, "true") || encryption == "1" {
		plaintext, err := DecryptSubscriptionBody(body, aesPassword)
		if err != nil {
			return nil, err
		}
		return []byte(plaintext), nil
	}
	return body, nil
}

func decryptAgeSubscription(ciphertext io.Reader, credential string) ([]byte, error) {
	if credential == "" {
		return nil, errors.New("AGE subscription requires a private key or passphrase")
	}
	// Mihomo accepts an age identity file (including the comments emitted by
	// keygen). A passphrase can be any string, including one with a key prefix.
	identities, _ := age.ParseIdentities(strings.NewReader(strings.TrimSpace(credential)))
	passphrase, err := age.NewScryptIdentity(credential)
	if err != nil {
		return nil, errors.New("invalid AGE subscription credential")
	}
	passphrase.SetMaxWorkFactor(18)
	identities = append(identities, subscriptionScryptIdentity{passphrase})
	reader, err := age.Decrypt(ciphertext, identities...)
	if err != nil {
		return nil, errors.New("cannot decrypt AGE subscription with the configured credential")
	}
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, errors.New("AGE subscription failed integrity verification")
	}
	return plaintext, nil
}
