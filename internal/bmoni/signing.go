package bmoni

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// OwnerKey is a secp256k1 key that can sign BMONI's owner-proof challenges
// (EIP-191) and raw 32-byte digests for transactions.
type OwnerKey struct {
	priv *ecdsa.PrivateKey
}

// NewOwnerKey generates a fresh secp256k1 owner key.
func NewOwnerKey() (*OwnerKey, error) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("bmoni: generate owner key: %w", err)
	}
	return &OwnerKey{priv: priv}, nil
}

// SignOwnerProof signs an owner-proof challenge message using EIP-191
// personal_sign semantics, returning a 0x-prefixed 65-byte signature and the
// derived owner address.
func (k *OwnerKey) SignOwnerProof(message string) (string, string, error) {
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, k.priv)
	if err != nil {
		return "", "", fmt.Errorf("bmoni: sign owner proof: %w", err)
	}

	return hexutil.Encode(sig), k.Address(), nil
}

// Address returns the checksummed owner address for the key.
func (k *OwnerKey) Address() string {
	return crypto.PubkeyToAddress(k.priv.PublicKey).Hex()
}

// Bytes returns the raw private key bytes for encrypted-at-rest storage.
func (k *OwnerKey) Bytes() []byte {
	return crypto.FromECDSA(k.priv)
}

// ParseOwnerKey reconstructs an owner key from its raw bytes.
func ParseOwnerKey(raw []byte) (*OwnerKey, error) {
	priv, err := crypto.ToECDSA(raw)
	if err != nil {
		return nil, fmt.Errorf("bmoni: parse owner key: %w", err)
	}
	return &OwnerKey{priv: priv}, nil
}

// SignDigest signs a raw 32-byte digest (used for BMONI proposal signing).
func (k *OwnerKey) SignDigest(digest []byte) (string, error) {
	sig, err := crypto.Sign(digest, k.priv)
	if err != nil {
		return "", fmt.Errorf("bmoni: sign digest: %w", err)
	}
	return hexutil.Encode(sig), nil
}
