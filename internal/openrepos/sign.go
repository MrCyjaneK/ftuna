package openrepos

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func verifyDetached(keyArmored, signed, signature []byte) (fingerprint string, err error) {
	ring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(keyArmored))
	if err != nil {
		return "", fmt.Errorf("read openrepos key: %w", err)
	}
	signer, err := openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(signed), bytes.NewReader(signature), nil)
	if err != nil {
		return "", fmt.Errorf("repomd.xml signature: %w", err)
	}
	if signer == nil || signer.PrimaryKey == nil {
		return "", fmt.Errorf("repomd.xml signature has no signer")
	}
	return hex.EncodeToString(signer.PrimaryKey.Fingerprint), nil
}
