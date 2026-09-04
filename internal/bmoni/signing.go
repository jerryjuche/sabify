package bmoni

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

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

// SignDigest signs a raw 32-byte digest and returns the 65-byte hex signature
// (r‖s‖v) with the recovery id normalised to 27/28, which is what BMONI's
// proposal flow expects. This is used for smart-wallet proposals ONLY — the
// owner-proof challenge at wallet creation wants the opposite (EIP-191 text
// signing via SignOwnerProof). Confusing the two is BMONI's most common
// signing mistake; the docs verify this vector in "Sign a proposal".
func (k *OwnerKey) SignDigest(digest []byte) (string, error) {
	sig, err := crypto.Sign(digest, k.priv)
	if err != nil {
		return "", fmt.Errorf("bmoni: sign digest: %w", err)
	}
	// go-ethereum returns the recovery id as 0/1; Ethereum signatures carry
	// it as 27/28 (0x1b/0x1c). BMONI rejects signatures with v = 0/1.
	sig[64] += 27
	return hexutil.Encode(sig), nil
}

// ---------------------------------------------------------------------------
// Owner-key storage (encrypted at rest)
// ---------------------------------------------------------------------------

// EncryptOwnerKey seals a raw secp256k1 key with AES-256-GCM. The encryption
// key is derived from BMONI_WALLET_ENCRYPTION_KEY: a 64-char hex string is
// used verbatim as the 32 bytes, anything else is SHA-256'd to 32 bytes. The
// returned blob is base64 (nonce ‖ ciphertext) and is safe to store in the
// bmoni_wallets table.
func EncryptOwnerKey(rawKey, encryptionKey []byte) (string, error) {
	if len(encryptionKey) == 0 {
		return "", fmt.Errorf("bmoni: wallet encryption key is not configured")
	}

	block, err := aes.NewCipher(deriveEncryptionKey(encryptionKey))
	if err != nil {
		return "", fmt.Errorf("bmoni: encryption cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("bmoni: gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("bmoni: nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, rawKey, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptOwnerKey reverses EncryptOwnerKey, returning the raw secp256k1 key
// bytes. The same BMONI_WALLET_ENCRYPTION_KEY must be configured.
func DecryptOwnerKey(blob string, encryptionKey []byte) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil {
		return nil, fmt.Errorf("bmoni: decode sealed key: %w", err)
	}

	block, err := aes.NewCipher(deriveEncryptionKey(encryptionKey))
	if err != nil {
		return nil, fmt.Errorf("bmoni: encryption cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("bmoni: gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("bmoni: sealed key too short")
	}

	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	raw, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("bmoni: decrypt owner key: %w", err)
	}
	return raw, nil
}

// deriveEncryptionKey normalises BMONI_WALLET_ENCRYPTION_KEY into 32 bytes.
func deriveEncryptionKey(key []byte) []byte {
	s := strings.TrimSpace(string(key))
	if len(s) == 64 {
		if raw, err := hex.DecodeString(s); err == nil {
			return raw
		}
	}
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
