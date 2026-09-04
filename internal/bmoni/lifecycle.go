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
	// The API wraps the user in a `user` object; the path-scoped identifier
	// other endpoints accept is `user.bmoniUserId` (the same value the
	// by-phone recovery returns). Fall back to `user.id` for safety.
	var resp struct {
		User struct {
			ID          string `json:"id"`
			BmoniUserID string `json:"bmoniUserId"`
		} `json:"user"`
	}
	err := c.do(ctx, "POST", "/v1/users", req, &resp)
	if err != nil {
		return "", err
	}
	if resp.User.BmoniUserID != "" {
		return resp.User.BmoniUserID, nil
	}
	if resp.User.ID != "" {
		return resp.User.ID, nil
	}
	return "", fmt.Errorf("bmoni: create-user response carried no user id")
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
		Address string `json:"walletAddress"`
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
	ID      string `json:"challengeId"`
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
// is what makes the persona match. walletAddress must be the provisioned
// wallet's real address (an empty string yields a confusing validation error
// asking for usdWalletAddress too — it does not actually want that field).
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
	// BVN, when set, is submitted as an identification number so the rail's
	// verification has it on the profile.
	BVN string
	// SourceOfFunds is one of the values from GET /kyc/options
	// (salary | business | investments | pension | government | inheritance
	// | savings). The API rejects free text here — always send a code.
	SourceOfFunds string
}

// SubmitKYC saves the KYC profile so the persona name matches for the BVN/rail.
// The live API rejects `addressDetails` and expects `address` — verified
// against the sandbox; the docs' curl examples are stale on this field name.
func (c *Client) SubmitKYC(ctx context.Context, userID string, profile KYCProfile) error {
	payload := map[string]interface{}{
		"personalInfo": map[string]interface{}{
			"firstName":   profile.FirstName,
			"lastName":    profile.LastName,
			"dateOfBirth": profile.DateOfBirth,
			"gender":      profile.Gender,
		},
		"address": map[string]interface{}{
			"streetLine1": profile.StreetLine1,
			"city":        profile.City,
			"state":       profile.State,
			"postalCode":  profile.PostalCode,
			"countryCode": profile.CountryCode,
		},
	}
	if profile.BVN != "" {
		payload["identificationNumbers"] = []map[string]string{
			{"type": "bvn", "number": profile.BVN, "issuingCountryCode": "NGA"},
		}
	}
	if profile.SourceOfFunds != "" {
		payload["sourceOfFunds"] = profile.SourceOfFunds
	}
	return c.do(ctx, "PATCH", "/v1/users/"+userID+"/kyc", payload, nil)
}

// BVNRecord is the holder details returned by the fetch-only BVN lookup. The
// docs recommend running this first and using its values to populate the KYC
// profile, which guarantees the persona name matches at verification time.
type BVNRecord struct {
	FirstName        string
	LastName         string
	MiddleName       string
	DateOfBirth      string
	Gender           string
	PhoneNumber      string
	ResidentialAddr  string
	StateOfResidence string
}

// LookupBVN returns the holder record for a BVN (fetch-only, writes nothing).
func (c *Client) LookupBVN(ctx context.Context, userID, bvn string) (BVNRecord, error) {
	var resp struct {
		FirstName        string `json:"firstName"`
		LastName         string `json:"lastName"`
		MiddleName       string `json:"middleName"`
		DateOfBirth      string `json:"dateOfBirth"`
		Gender           string `json:"gender"`
		PhoneNumber      string `json:"phoneNumber"`
		ResidentialAddr  string `json:"residentialAddress"`
		StateOfResidence string `json:"stateOfResidence"`
	}
	err := c.do(ctx, "GET", "/v1/users/"+userID+"/kyc/bvn-lookup/"+bvn, nil, &resp)
	if err != nil {
		return BVNRecord{}, err
	}
	return BVNRecord{
		FirstName:        resp.FirstName,
		LastName:         resp.LastName,
		MiddleName:       resp.MiddleName,
		DateOfBirth:      resp.DateOfBirth,
		Gender:           resp.Gender,
		PhoneNumber:      resp.PhoneNumber,
		ResidentialAddr:  resp.ResidentialAddr,
		StateOfResidence: resp.StateOfResidence,
	}, nil
}

// BVNLookup confirms a BVN is recognised by BMONI's sandbox. Fetch-only. The
// path is scoped to the user and the persona's number must be used.
func (c *Client) BVNLookup(ctx context.Context, userID, bvn string) error {
	_, err := c.LookupBVN(ctx, userID, bvn)
	return err
}

// ActivateKYC runs full verification against the submitted profile. The live
// API requires a sumsubLevelName even for NGN (verified in sandbox; e.g.
// "id-and-liveness" activates a persona). Docs claim NGN omits the body —
// that is stale; send a level.
func (c *Client) ActivateKYC(ctx context.Context, userID, sumsubLevelName string) error {
	return c.do(ctx, "POST", "/v1/users/"+userID+"/kyc/activate", map[string]interface{}{
		"sumsubLevelName": sumsubLevelName,
	}, nil)
}

// KycReadiness reports whether the profile + documents are complete enough to
// activate, plus what is missing.
type KycReadiness struct {
	Ready   bool
	Missing []string
}

func (c *Client) KycReadiness(ctx context.Context, userID string) (KycReadiness, error) {
	var resp struct {
		Ready   bool     `json:"ready"`
		Missing []string `json:"missing"`
	}
	err := c.do(ctx, "GET", "/v1/users/"+userID+"/kyc/readiness", nil, &resp)
	if err != nil {
		return KycReadiness{}, err
	}
	return KycReadiness{Ready: resp.Ready, Missing: resp.Missing}, nil
}

// UploadKycDocument uploads one KYC document image (multipart). kind is one of
// identification | proof-of-address | biometric. The endpoint-specific field
// names are handled here so callers pass a plain file.
func (c *Client) UploadKycDocument(ctx context.Context, userID, kind string, file []byte, filename string, extra map[string]string) error {
	// /v1/users/{id}/kyc/documents/{identification|proof-of-address|biometric}
	return c.doMultipart(ctx, "POST", "/v1/users/"+userID+"/kyc/documents/"+kind, file, filename, extra)
}

// OnboardingStatus returns the anchor (NGN) status for a user.
func (c *Client) OnboardingStatus(ctx context.Context, userID string) (map[string]string, error) {
	var resp map[string]string
	err := c.do(ctx, "GET", "/v1/users/"+userID+"/onboarding/status", nil, &resp)
	return resp, err
}

// OwnerWallet holds the platform's secp256k1 owner private key. It signs the
// EIP-191 owner-proof challenge with the same key material BMONI's Go examples
// use, and returns both the 0x-prefixed signature and the derived address.
type OwnerWallet interface {
	SignOwnerProof(message string) (signature, signerAddress string, err error)
	// Address returns the owner wallet's checksummed Ethereum address.
	Address() string
}

// WaitForVBA polls DepositAccount until BMONI provisions the number for the
// user (exposed for handlers/tools that provision the NGN rail synchronously).
func (c *Client) WaitForVBA(ctx context.Context, userID string, attempts int) (DepositAccount, error) {
	return waitForVBA(ctx, c, userID, attempts)
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
