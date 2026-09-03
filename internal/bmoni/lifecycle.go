package bmoni

import (
	"context"
	"fmt"
	"time"
)

// CreateUser registers a user with BMONI and returns the bmoni user id.
// A 409 (duplicate email/phone) means the user already exists — recover rather
// than retry by looking up an existing id.
type CreateUserRequest struct {
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phoneNumber"`
	BVN         string `json:"bvn,omitempty"`
}

func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, "POST", "/v1/users", req, &resp)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// CreateWallet provisions a smart wallet for a user. It requests an owner-proof
// challenge (scoped to the owner address + currency), signs it (EIP-191), and
// deploys a managed wallet. The challenge is single-use and expires in 10
// minutes; a fresh challenge is requested on retry.
func (c *Client) CreateWallet(ctx context.Context, userID string, wallet OwnerWallet) (smartWalletID, address string, err error) {
	challenge, err := c.ownerProofChallenge(ctx, userID, wallet.Address())
	if err != nil {
		return "", "", err
	}

	signature, signer, err := wallet.SignOwnerProof(challenge.Message)
	if err != nil {
		return "", "", fmt.Errorf("bmoni: sign owner proof: %w", err)
	}

	var createResp struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}

	err = c.do(ctx, "POST", "/v1/users/"+userID+"/smart-wallets/create-managed", map[string]interface{}{
		"currency":              "CNGN",
		"userOwnerAddress":      signer,
		"ownerProofChallengeId": challenge.ID,
		"ownerProofSignature":   signature,
	}, &createResp)
	if err != nil {
		return "", "", fmt.Errorf("bmoni: create-managed: %w", err)
	}

	return createResp.ID, createResp.Address, nil
}

type ownerProofChallenge struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func (c *Client) ownerProofChallenge(ctx context.Context, userID, ownerAddress string) (ownerProofChallenge, error) {
	var resp ownerProofChallenge
	err := c.do(ctx, "POST", "/v1/users/"+userID+"/smart-wallets/owner-proof-challenges", map[string]interface{}{
		"currency":         "CNGN",
		"userOwnerAddress": ownerAddress,
	}, &resp)
	return resp, err
}

// StartNigeria activates the NGN rail for a user, issuing their NGN virtual
// bank account (VBA). `bvn` is required and, paired with the saved KYC profile,
// is what makes the persona match.
func (c *Client) StartNigeria(ctx context.Context, userID, bvn, walletAddress string) error {
	return c.do(ctx, "POST", "/v1/users/"+userID+"/onboarding/start-nigeria", map[string]interface{}{
		"bvn":              bvn,
		"ngnWalletAddress": walletAddress,
		"ngnWalletIndex":   0,
	}, nil)
}

// KYCProfile is the single Nigerian-resident KYC profile we submit for the
// platform persona. It matches the documented `PATCH /v1/users/{id}/kyc` shape.
type KYCProfile struct {
	FirstName   string
	LastName    string
	DateOfBirth string
	Gender      string
	StreetLine1 string
	City        string
	State       string
	PostalCode  string
	CountryCode string
}

// SubmitKYC saves the KYC profile so the persona name matches for the BVN/rail.
func (c *Client) SubmitKYC(ctx context.Context, userID string, profile KYCProfile) error {
	return c.do(ctx, "PATCH", "/v1/users/"+userID+"/kyc", map[string]interface{}{
		"personalInfo": map[string]interface{}{
			"firstName":   profile.FirstName,
			"lastName":    profile.LastName,
			"dateOfBirth": profile.DateOfBirth,
			"gender":      profile.Gender,
		},
		"addressDetails": map[string]interface{}{
			"streetLine1": profile.StreetLine1,
			"city":        profile.City,
			"state":       profile.State,
			"postalCode":  profile.PostalCode,
			"countryCode": profile.CountryCode,
		},
	}, nil)
}

// BVNLookup confirms a BVN is recognised by BMONI's sandbox. Fetch-only. The
// path is scoped to the user and the persona's number must be used.
func (c *Client) BVNLookup(ctx context.Context, userID, bvn string) error {
	return c.do(ctx, "GET", "/v1/users/"+userID+"/kyc/bvn-lookup/"+bvn, nil, nil)
}

// ActivateKYC runs full verification against the submitted profile. This
// matches the persona; it is the trigger for the (USD) EDD account.
func (c *Client) ActivateKYC(ctx context.Context, userID string) error {
	return c.do(ctx, "POST", "/v1/users/"+userID+"/kyc/activate", nil, nil)
}

// OwnerWallet holds the platform's secp256k1 owner private key. It signs the
// EIP-191 owner-proof challenge with the same key material BMONI's Go examples
// use, and returns both the 0x-prefixed signature and the derived address.
type OwnerWallet interface {
	SignOwnerProof(message string) (signature, signerAddress string, err error)
	// Address returns the owner wallet's checksummed Ethereum address.
	Address() string
}

// waitForVBA polls DepositAccount until BMONI provisions the number.
func waitForVBA(ctx context.Context, c *Client, userID string, attempts int) (DepositAccount, error) {
	var last DepositAccount
	for i := 0; i < attempts; i++ {
		account, err := c.DepositAccount(ctx, userID)
		if err == nil && account.AccountNumber != "" {
			return account, nil
		}
		last = account
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return last, fmt.Errorf("bmoni: VBA not provisioned after %d attempts", attempts)
}
