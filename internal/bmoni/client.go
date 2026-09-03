package bmoni

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Balances returns the wallet balances for a BMONI user.
func (c *Client) Balances(ctx context.Context, userID string) ([]Balance, error) {
	var resp struct {
		Balances []Balance `json:"balances"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/balances", nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

type Balance struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// DepositAccount returns the NGN deposit (virtual bank) account for a user.
func (c *Client) DepositAccount(ctx context.Context, userID string) (DepositAccount, error) {
	var resp struct {
		Account DepositAccount `json:"account"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/users/"+userID+"/bank-accounts/deposit-accounts/NGN", nil, &resp)
	return resp.Account, err
}

type DepositAccount struct {
	AccountNumber string `json:"accountNumber"`
	BankName      string `json:"bankName"`
}
