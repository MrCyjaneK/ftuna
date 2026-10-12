// Package sign loads and uses the GPG key in FTUNA_OSS_GPG_PRIVATE_KEY.
package sign

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

const EnvKey = "FTUNA_OSS_GPG_PRIVATE_KEY"

func generate() (string, error) {
	entity, err := openpgp.NewEntity("FTuna OSS", "test", "ftuna@localhost", nil)
	if err != nil {
		return "", err
	}
	return armorPrivate(entity)
}

func armorPrivate(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", err
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func EntityFromEnv() (*openpgp.Entity, error) {
	b64 := strings.TrimSpace(os.Getenv(EnvKey))
	if b64 == "" {
		return nil, fmt.Errorf("%s is not set", EnvKey)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvKey, err)
	}
	return readPrivate(raw)
}

func readPrivate(armored []byte) (*openpgp.Entity, error) {
	el, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armored))
	if err != nil {
		return nil, err
	}
	if len(el) == 0 || el[0].PrivateKey == nil {
		return nil, fmt.Errorf("key has no private material")
	}
	entity := el[0]
	if entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Decrypt([]byte{}); err != nil {
			return nil, fmt.Errorf("decrypt key: %w", err)
		}
	}
	return entity, nil
}

func PublicArmor(entity *openpgp.Entity) (string, error) {
	return publicArmor(entity)
}

func Sign(entity *openpgp.Entity, message []byte) (string, error) {
	return signMessage(entity, message)
}
