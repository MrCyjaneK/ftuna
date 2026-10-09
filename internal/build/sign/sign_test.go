package sign

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestSignRoundTrip(t *testing.T) {
	priv, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	entity, err := readPrivate([]byte(priv))
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("<repomd>test</repomd>")
	sig, err := Sign(entity, msg)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := PublicArmor(entity)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(pub))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openpgp.CheckArmoredDetachedSignature(ring, bytes.NewReader(msg), strings.NewReader(sig), nil); err != nil {
		t.Fatal(err)
	}
}

func TestEntityFromEnvMissing(t *testing.T) {
	t.Setenv(EnvKey, "")
	os.Unsetenv(EnvKey)
	if _, err := EntityFromEnv(); err == nil {
		t.Fatal("expected missing key to fail")
	}
}
