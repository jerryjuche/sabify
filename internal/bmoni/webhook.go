package bmoni

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifyWebhookSignature checks the X-Webhook-Signature header against the raw
// request body using HMAC-SHA256 keyed with the webhook secret. The signature
// must be verified over the exact raw bytes, not a re-serialized payload.
func VerifyWebhookSignature(rawBody []byte, signature, secret string) bool {
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	return hmac.Equal(sigBytes, expected)
}
