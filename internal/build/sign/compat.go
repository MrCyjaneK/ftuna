package sign

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash"
	"math/big"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// Sailfish OS 5.1 ships GnuPG 2.0.4. A hashed issuer-key-id subpacket makes
// that GnuPG reject the signature, so signatures here match the packets GnuPG
// itself writes: SHA1, and the issuer key id in the unhashed area.

func signMessage(entity *openpgp.Entity, message []byte) (string, error) {
	priv, _, err := signingMaterial(entity)
	if err != nil {
		return "", err
	}
	body, err := detachedSignature(priv, entity.PrimaryKey, message, time.Now())
	if err != nil {
		return "", err
	}
	return armorBytes(openpgp.SignatureType, body)
}

func publicArmor(entity *openpgp.Entity) (string, error) {
	priv, userid, err := signingMaterial(entity)
	if err != nil {
		return "", err
	}
	keyBody, err := publicKeyBody(entity.PrimaryKey)
	if err != nil {
		return "", err
	}
	sig, err := selfSignature(priv, keyBody, []byte(userid), entity.PrimaryKey.CreationTime)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	buf.Write(oldPacket(6, keyBody))
	buf.Write(oldPacket(13, []byte(userid)))
	buf.Write(sig)
	return armorBytes(openpgp.PublicKeyType, buf.Bytes())
}

func signingMaterial(entity *openpgp.Entity) (*rsa.PrivateKey, string, error) {
	if entity == nil || entity.PrimaryKey == nil || entity.PrivateKey == nil {
		return nil, "", fmt.Errorf("key has no signing key")
	}
	priv, ok := entity.PrivateKey.PrivateKey.(*rsa.PrivateKey)
	if !ok || priv == nil {
		return nil, "", fmt.Errorf("signing key is not RSA")
	}
	for _, id := range entity.Identities {
		if id == nil || id.UserId == nil || id.UserId.Id == "" {
			continue
		}
		if id.SelfSignature != nil && id.SelfSignature.IsPrimaryId != nil && *id.SelfSignature.IsPrimaryId {
			return priv, id.UserId.Id, nil
		}
	}
	for _, id := range entity.Identities {
		if id != nil && id.UserId != nil && id.UserId.Id != "" {
			return priv, id.UserId.Id, nil
		}
	}
	return nil, "", fmt.Errorf("key has no user id")
}

func publicKeyBody(pk *packet.PublicKey) ([]byte, error) {
	var buf bytes.Buffer
	if err := pk.Serialize(&buf); err != nil {
		return nil, err
	}
	body, err := packetBody(buf.Bytes())
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(append(append([]byte{0x99}, byte(len(body)>>8), byte(len(body))), body...))
	if !bytes.Equal(sum[:], pk.Fingerprint) {
		return nil, fmt.Errorf("public key packet does not match its fingerprint")
	}
	return body, nil
}

func detachedSignature(priv *rsa.PrivateKey, pk *packet.PublicKey, message []byte, created time.Time) ([]byte, error) {
	hashed := subpacket(2, unix(created))
	unhashed := subpacket(16, keyID(pk))
	head := signatureHead(0x00, hashed)
	h := sha1.New()
	h.Write(message)
	return finishSignature(priv, h, head, unhashed)
}

func selfSignature(priv *rsa.PrivateKey, keyBody, userid []byte, created time.Time) ([]byte, error) {
	hashed := bytes.Join([][]byte{
		subpacket(2, unix(created)),
		subpacket(27, []byte{0x2f}),
		subpacket(11, []byte{9, 8, 7, 3, 2, 1}),
		subpacket(21, []byte{2, 8, 3}),
		subpacket(22, []byte{2, 3, 1}),
		subpacket(30, []byte{1}),
		subpacket(23, []byte{0x80}),
	}, nil)
	unhashed := subpacket(16, fingerprintKeyID(sha1.Sum(append([]byte{0x99, byte(len(keyBody) >> 8), byte(len(keyBody))}, keyBody...))))
	head := signatureHead(0x13, hashed)
	h := sha1.New()
	h.Write([]byte{0x99, byte(len(keyBody) >> 8), byte(len(keyBody))})
	h.Write(keyBody)
	h.Write([]byte{0xb4})
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(userid)))
	h.Write(n[:])
	h.Write(userid)
	return finishSignature(priv, h, head, unhashed)
}

func signatureHead(class byte, hashed []byte) []byte {
	head := []byte{4, class, 1, 2}
	head = append(head, byte(len(hashed)>>8), byte(len(hashed)))
	return append(head, hashed...)
}

func finishSignature(priv *rsa.PrivateKey, h hash.Hash, head, unhashed []byte) ([]byte, error) {
	h.Write(head)
	var hashedLen [4]byte
	binary.BigEndian.PutUint32(hashedLen[:], uint32(len(head)))
	h.Write([]byte{4, 0xff})
	h.Write(hashedLen[:])
	digest := h.Sum(nil)
	sig, err := rsa.SignPKCS1v15(nil, priv, crypto.SHA1, digest)
	if err != nil {
		return nil, err
	}
	body := append(append([]byte{}, head...), byte(len(unhashed)>>8), byte(len(unhashed)))
	body = append(body, unhashed...)
	body = append(body, digest[0], digest[1])
	body = append(body, mpi(new(big.Int).SetBytes(sig))...)
	return oldPacket(2, body), nil
}

func keyID(pk *packet.PublicKey) []byte {
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], pk.KeyId)
	return id[:]
}

func fingerprintKeyID(sum [20]byte) []byte {
	return sum[12:]
}

func unix(t time.Time) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(t.Unix()))
	return b[:]
}

func subpacket(kind byte, content []byte) []byte {
	body := append([]byte{kind}, content...)
	if len(body) >= 192 {
		panic("signature subpacket too long")
	}
	return append([]byte{byte(len(body))}, body...)
}

func mpi(i *big.Int) []byte {
	raw := i.Bytes()
	if len(raw) == 0 {
		raw = []byte{0}
	}
	var bits [2]byte
	binary.BigEndian.PutUint16(bits[:], uint16(i.BitLen()))
	return append(bits[:], raw...)
}

func oldPacket(tag byte, body []byte) []byte {
	if len(body) >= 65536 {
		panic("packet too long")
	}
	hdr := []byte{0x80 | (tag << 2) | 1, byte(len(body) >> 8), byte(len(body))}
	return append(hdr, body...)
}

func packetBody(pkt []byte) ([]byte, error) {
	if len(pkt) < 2 || pkt[0]&0x80 == 0 {
		return nil, fmt.Errorf("bad packet")
	}
	if pkt[0]&0x40 == 0 {
		switch pkt[0] & 3 {
		case 0:
			n := int(pkt[1])
			return pkt[2 : 2+n], nil
		case 1:
			n := int(pkt[1])<<8 | int(pkt[2])
			return pkt[3 : 3+n], nil
		default:
			return nil, fmt.Errorf("unsupported packet length")
		}
	}
	b := pkt[1]
	switch {
	case b < 192:
		return pkt[2 : 2+int(b)], nil
	case b < 224:
		n := (int(b)-192)<<8 + int(pkt[2]) + 192
		return pkt[3 : 3+n], nil
	default:
		return nil, fmt.Errorf("unsupported packet length")
	}
}

func armorBytes(kind string, body []byte) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, kind, nil)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(body); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
