package bmoni

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("bmoni: encode request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("bmoni: build request: %w", err)
	}

	req.Header.Set("x-api-key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("bmoni: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("bmoni: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("bmoni: decode response: %w", err)
	}

	return nil
}

// ResolveUserByPhone resolves a partner's existing user from an E.164 phone
// number and returns their smart wallets. This is the sanctioned recovery
// path for the "user already exists" 409 on POST /v1/users — see BMONI's
// "Retries and duplicates" doc. A 404 means no user with that phone belongs
// to this partner.
type ResolvedUser struct {
	UserID  string
	Wallets []ResolvedWallet
}

type ResolvedWallet struct {
	ID       string
	Currency string
	Address  string
	IsActive bool
}

func (c *Client) ResolveUserByPhone(ctx context.Context, phoneNumber string) (ResolvedUser, error) {
	var resp struct {
		BmoniUserID string `json:"bmoniUserId"`
		Wallets     []struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
			Address  string `json:"walletAddress"`
			IsActive bool   `json:"isActive"`
		} `json:"wallets"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/smart-wallets/by-phone?phoneNumber="+url.QueryEscape(phoneNumber), nil, &resp)
	if err != nil {
		return ResolvedUser{}, err
	}
	out := ResolvedUser{UserID: resp.BmoniUserID}
	for _, w := range resp.Wallets {
		out.Wallets = append(out.Wallets, ResolvedWallet{
			ID: w.ID, Currency: w.Currency, Address: w.Address, IsActive: w.IsActive,
		})
	}
	return out, nil
}

// SmartWallet is a wallet on a BMONI user, as listed by the account-wallets
// endpoint. One CNGN wallet per user — never call create-managed when one
// already exists (409 E502).
type SmartWallet struct {
	ID       string
	Currency string
	Address  string
	IsActive bool
}

// ListWallets returns every smart wallet on a user (GET
// /smart-wallets/account/wallets). Used before provisioning so a second
// create-managed is never attempted for a currency that already has a wallet.
func (c *Client) ListWallets(ctx context.Context, userID string) ([]SmartWallet, error) {
	var resp []struct {
		ID            string `json:"id"`
		Currency      string `json:"currency"`
		WalletAddress string `json:"walletAddress"`
		IsActive      bool   `json:"isActive"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/smart-wallets/account/wallets", nil, &resp)
	if err != nil {
		return nil, err
	}
	out := make([]SmartWallet, 0, len(resp))
	for _, w := range resp {
		out = append(out, SmartWallet{ID: w.ID, Currency: w.Currency, Address: w.WalletAddress, IsActive: w.IsActive})
	}
	return out, nil
}

// Balances returns the wallet balances for a BMONI user.
func (c *Client) Balances(ctx context.Context, userID string) ([]Balance, error) {
	var resp struct {
		Balances []Balance `json:"balances"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/smart-wallets/account/balances", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

// WalletTransaction is one entry of the wallet's transaction history.
type WalletTransaction struct {
	ID          string
	Type        string
	Status      string
	Amount      string
	Currency    string
	Description string
	CreatedAt   string
}

// WalletTransactions returns the smart wallet's transaction history
// (GET /smart-wallets/{smartWalletId}/transactions).
func (c *Client) WalletTransactions(ctx context.Context, userID, smartWalletID string) ([]WalletTransaction, error) {
	var resp struct {
		Data struct {
			Transactions []struct {
				ID          string      `json:"id"`
				Type        string      `json:"type"`
				Status      string      `json:"status"`
				Amount      string      `json:"amount"`
				Currency    string      `json:"currency"`
				Description interface{} `json:"description"`
				CreatedAt   interface{} `json:"createdAt"`
			} `json:"transactions"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/smart-wallets/"+smartWalletID+"/transactions", nil, &resp)
	if err != nil {
		return nil, err
	}
	out := make([]WalletTransaction, 0, len(resp.Data.Transactions))
	for _, t := range resp.Data.Transactions {
		out = append(out, WalletTransaction{
			ID: t.ID, Type: t.Type, Status: t.Status,
			Amount: t.Amount, Currency: t.Currency,
			Description: fmt.Sprintf("%v", t.Description),
			CreatedAt:   fmt.Sprintf("%v", t.CreatedAt),
		})
	}
	return out, nil
}

type Balance struct {
	Currency string `json:"currency"`
	// The API reports the amount under "balance" on the balances endpoint;
	// keep "amount" as a fallback for other shapes.
	Amount string `json:"amount"`
	Value  string `json:"balance"`
}

// DepositAccount returns the NGN deposit (virtual bank) account for a user.
// The endpoint returns {"accounts":[...]}; we take the first NGN account.
func (c *Client) DepositAccount(ctx context.Context, userID string) (DepositAccount, error) {
	var resp struct {
		Accounts []DepositAccount `json:"accounts"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/bank-accounts/deposit-accounts/NGN", nil, &resp)
	if err != nil {
		return DepositAccount{}, err
	}
	if len(resp.Accounts) == 0 {
		return DepositAccount{}, fmt.Errorf("bmoni: no NGN deposit account provisioned")
	}
	return resp.Accounts[0], nil
}

type DepositAccount struct {
	AccountNumber string `json:"accountNumber"`
	BankName      string `json:"bankName"`
}

// ---------------------------------------------------------------------------
// Money out: Nigerian bank withdrawals (verify → register → offramp → sign)
// ---------------------------------------------------------------------------

// doMultipart posts a single-file multipart form. The `files`/`selfie` field
// name is chosen per KYC document kind: identification and proof-of-address
// use "files", biometric uses "selfie". extra carries the other form fields.
func (c *Client) doMultipart(ctx context.Context, method, path string, file []byte, filename string, extra map[string]string) error {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	field := "files"
	if kind := path[strings.LastIndex(path, "/")+1:]; kind == "biometric" {
		field = "selfie"
	}

	// The file part MUST carry a real image Content-Type — BMONI rejects
	// application/octet-stream with E101 (verified live). Sniff the magic
	// bytes so PNG/JPEG uploads are accepted regardless of filename.
	contentType := "application/octet-stream"
	if len(file) >= 4 && file[0] == 0x89 && file[1] == 'P' && file[2] == 'N' && file[3] == 'G' {
		contentType = "image/png"
	} else if len(file) >= 3 && file[0] == 0xFF && file[1] == 0xD8 && file[2] == 0xFF {
		contentType = "image/jpeg"
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", contentType)
	part, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("bmoni: multipart field: %w", err)
	}
	if _, err := part.Write(file); err != nil {
		return fmt.Errorf("bmoni: multipart write: %w", err)
	}
	for k, v := range extra {
		if err := w.WriteField(k, v); err != nil {
			return fmt.Errorf("bmoni: multipart field %s: %w", k, err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("bmoni: multipart close: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("bmoni: build multipart request: %w", err)
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("bmoni: multipart request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("bmoni: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// BindVBA points the user's NGN deposit account (VBA) at a specific smart
// wallet. Needed when a fresh wallet replaces the original one, so deposits
// land in the wallet whose owner key we hold.
func (c *Client) BindVBA(ctx context.Context, userID, smartWalletID string) error {
	return c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/smart-wallets/"+smartWalletID+"/onramp/vba/nigeria", map[string]interface{}{}, nil)
}

// NigerianBank is a supported bank returned by the nigerian-banks listing.
// The live API reports {bankName, bankCode} — the docs' {name, code} shape
// decodes to empty values and every code lookup then fails.
type NigerianBank struct {
	Name string `json:"bankName"`
	Code string `json:"bankCode"`
}

// NigerianBanks lists every supported bank with its CBN code.
func (c *Client) NigerianBanks(ctx context.Context, userID string) ([]NigerianBank, error) {
	var resp struct {
		Banks []NigerianBank `json:"banks"`
		Data  []NigerianBank `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/bank-accounts/nigerian-banks", nil, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Banks) > 0 {
		return resp.Banks, nil
	}
	return resp.Data, nil
}

// VerifiedAccount is the account-holder record returned by
// verify-nigerian-account. Only the holder name is needed downstream.
type VerifiedAccount struct {
	AccountHolderName string
}

// VerifyNigerianAccount resolves an account number + CBN bank code into the
// registered holder name. A 404 means no account matches — surface that to
// the user rather than pushing on.
func (c *Client) VerifyNigerianAccount(ctx context.Context, userID, accountNumber, bankCode string) (VerifiedAccount, error) {
	var resp struct {
		Name              string `json:"name"`
		AccountHolderName string `json:"accountHolderName"`
		AccountName       string `json:"accountName"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/bank-accounts/verify-nigerian-account", map[string]string{
		"accountNumber": accountNumber,
		"bankCode":      bankCode,
	}, &resp)
	if err != nil {
		return VerifiedAccount{}, err
	}

	holder := resp.AccountHolderName
	if holder == "" {
		holder = resp.Name
	}
	if holder == "" {
		holder = resp.AccountName
	}
	return VerifiedAccount{AccountHolderName: holder}, nil
}

// RegisterWithdrawalAccount saves a verified Nigerian account and returns the
// bankAccountId used by the offramp call. Get-or-create: repeating the same
// account returns the existing record.
func (c *Client) RegisterWithdrawalAccount(ctx context.Context, userID string, account VerifiedAccount, accountNumber, bankCode, bankName string) (string, error) {
	var resp struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/bank-accounts/withdrawal-accounts/nigeria", map[string]string{
		"accountNumber":      accountNumber,
		"bankCode":           bankCode,
		"bankName":           bankName,
		"accountHolderName":  account.AccountHolderName,
	}, &resp)
	if err != nil {
		return "", err
	}
	if resp.ID != "" {
		return resp.ID, nil
	}
	return resp.Data.ID, nil
}

// CreateOfframp starts a bank payout proposal (CNGN → Nigerian bank). It
// returns the proposal id; nothing moves until the proposal is approved and
// signed by the wallet owner key.
func (c *Client) CreateOfframp(ctx context.Context, userID, smartWalletID, bankAccountID, fromAmount string) (string, error) {
	var resp struct {
		Data struct {
			ProposalID string `json:"proposalId"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/smart-wallets/"+smartWalletID+"/offramp/nigeria", map[string]string{
		"bankAccountId": bankAccountID,
		"fromAmount":    fromAmount,
	}, &resp)
	if err != nil {
		return "", err
	}
	return resp.Data.ProposalID, nil
}

// ApproveProposal records the calling admin's approval vote on a proposal.
func (c *Client) ApproveProposal(ctx context.Context, userID, proposalID string) error {
	return c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/smart-wallets/proposals/"+proposalID+"/approve", nil, nil)
}

// ProposalSignPayload is the digest a proposal must be signed over. Sign
// hashToSign with the owner key (raw hash, NOT EIP-191); it is ready only
// once the proposal reaches PENDING_SIGNATURES.
type ProposalSignPayload struct {
	HashToSign string `json:"hashToSign"`
	Deadline   string `json:"deadline"`
}

// SignPayload fetches the digest for a proposal.
func (c *Client) SignPayload(ctx context.Context, userID, proposalID string) (ProposalSignPayload, error) {
	var resp struct {
		Data ProposalSignPayload `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/smart-wallets/proposals/"+proposalID+"/sign-payload", nil, &resp)
	if err != nil {
		return ProposalSignPayload{}, err
	}
	return resp.Data, nil
}

// SubmitProposalSignature submits the 65-byte hex signature (r‖s‖v with
// v = 27/28) over the proposal digest.
func (c *Client) SubmitProposalSignature(ctx context.Context, userID, proposalID, signature string) error {
	return c.do(ctx, http.MethodPost, "/v1/users/"+userID+"/smart-wallets/proposals/"+proposalID+"/sign", map[string]string{
		"signature": signature,
	}, nil)
}

// ProposalStatus reads a proposal's lifecycle status.
type ProposalStatus struct {
	Status string `json:"status"`
}

// GetProposal returns the current status of a proposal.
func (c *Client) GetProposal(ctx context.Context, userID, proposalID string) (ProposalStatus, error) {
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/smart-wallets/proposals/"+proposalID, nil, &resp)
	if err != nil {
		return ProposalStatus{}, err
	}
	if resp.Status != "" {
		return ProposalStatus{Status: resp.Status}, nil
	}
	return ProposalStatus{Status: resp.Data.Status}, nil
}
