package bmoni

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// The values below are BMONI's published known-good test vector ("Sign a
// proposal"): digest = keccak256 of a fixed preimage, signed with the public
// Anvil/Hardhat test key. Reproducing the exact bytes offline proves our
// signing path (raw hash, v normalised to 27/28) matches what BMONI accepts.
const (
	testVectorPreimage = "bmoni-embedded:BKE-2041:sign-payload-example"
	testVectorPrivate  = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	testVectorHash     = "0x8f5156823a5c2cdc7bedc12253e49e4946c6fff0273034eb485750035d21ad31"
	testVectorSig      = "0x628f1aff48c9d1f35d45a735eb026db0437c5ed334a94dc7fb0ac86ca32c10bd" +
		"173a653a7f064c4512244f6fcbefb07e13bfe7368fcacdcc4e6fb153f50050991b"
	testVectorAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
)

func TestSignDigestReproducesBMONITestVector(t *testing.T) {
	hash := crypto.Keccak256Hash([]byte(testVectorPreimage))
	if got := hash.Hex(); got != testVectorHash {
		t.Fatalf("digest = %s, want %s", got, testVectorHash)
	}

	priv, err := crypto.ToECDSA(hexutil.MustDecode(testVectorPrivate))
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	key := &OwnerKey{priv: priv}
	sig, err := key.SignDigest(hash.Bytes())
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}
	if sig != testVectorSig {
		t.Fatalf("signature = %s\nwant        %s", sig, testVectorSig)
	}

	// The recovery id must be 27/28 (0x1b/0x1c), never 0/1 — v=0/1 is a
	// documented rejection reason.
	sigBytes, _ := hexutil.Decode(sig)
	if v := sigBytes[64]; v != 0x1b {
		t.Errorf("v byte = %d, want 27 (0x1b)", v)
	}
}

func TestSignDigestRecoversOwnerAddress(t *testing.T) {
	priv, _ := crypto.ToECDSA(hexutil.MustDecode(testVectorPrivate))
	key := &OwnerKey{priv: priv}

	hash := crypto.Keccak256Hash([]byte(testVectorPreimage))
	sig, err := key.SignDigest(hash.Bytes())
	if err != nil {
		t.Fatalf("SignDigest: %v", err)
	}

	sigBytes, _ := hexutil.Decode(sig)
	// go-ethereum's Ecrecover expects v ∈ {0,1}; normalise back for recovery.
	sigBytes[64] -= 27
	pub, err := crypto.Ecrecover(hash.Bytes(), sigBytes)
	if err != nil {
		t.Fatalf("ecrecover: %v", err)
	}
	pubKey, err := crypto.UnmarshalPubkey(pub)
	if err != nil {
		t.Fatalf("unmarshal pubkey: %v", err)
	}
	addr := crypto.PubkeyToAddress(*pubKey).Hex()
	if !strings.EqualFold(addr, testVectorAddress) {
		t.Fatalf("recovered address = %s, want %s", addr, testVectorAddress)
	}
}

func TestOwnerKeyEncryptDecryptRoundTrip(t *testing.T) {
	key, err := NewOwnerKey()
	if err != nil {
		t.Fatalf("NewOwnerKey: %v", err)
	}
	raw := key.Bytes()
	address := key.Address()

	const encKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 64 hex chars

	blob, err := EncryptOwnerKey(raw, []byte(encKey))
	if err != nil {
		t.Fatalf("EncryptOwnerKey: %v", err)
	}
	if blob == "" || blob == string(raw) {
		t.Fatal("encrypted blob must differ from the raw key")
	}

	decrypted, err := DecryptOwnerKey(blob, []byte(encKey))
	if err != nil {
		t.Fatalf("DecryptOwnerKey: %v", err)
	}
	if hex.EncodeToString(decrypted) != hex.EncodeToString(raw) {
		t.Fatal("decrypted key does not match the original")
	}

	reparsed, err := ParseOwnerKey(decrypted)
	if err != nil {
		t.Fatalf("ParseOwnerKey: %v", err)
	}
	if reparsed.Address() != address {
		t.Fatalf("address after round-trip = %s, want %s", reparsed.Address(), address)
	}
}

func TestDecryptOwnerKeyWithWrongKeyFails(t *testing.T) {
	key, err := NewOwnerKey()
	if err != nil {
		t.Fatalf("NewOwnerKey: %v", err)
	}

	const encKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	blob, err := EncryptOwnerKey(key.Bytes(), []byte(encKey))
	if err != nil {
		t.Fatalf("EncryptOwnerKey: %v", err)
	}

	if _, err := DecryptOwnerKey(blob, []byte("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")); err == nil {
		t.Fatal("expected an error decrypting with the wrong key")
	}
}

func TestEncryptOwnerKeyRequiresEncryptionKey(t *testing.T) {
	key, err := NewOwnerKey()
	if err != nil {
		t.Fatalf("NewOwnerKey: %v", err)
	}
	if _, err := EncryptOwnerKey(key.Bytes(), nil); err == nil {
		t.Fatal("expected an error when no encryption key is configured")
	}
}