/*
	tools/bmoni-bootstrap
	=====================

	Provisions the platform-owned BMONI wallet: user -> smart wallet -> KYC ->
	NGN rail, then persists the result into the bmoni_wallets table.

	Usage (from the repo root):

	    BMONI_API_KEY=... go run ./tools/bmoni-bootstrap

	It uses the sandbox "Bunch Dillon" persona by default so KYC passes
	automatically. Re-running is idempotent and recovers existing state rather
	than forking history.

	This is a setup/ops tool. It is never exercised by the running app.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"sabify/internal/bmoni"
	"sabify/internal/models"
)

const (
	personaFirstName = "Bunch"
	personaLastName  = "Dillon"
	personaEmail     = "platform@sabify.example"
	personaPhone     = "+2348000000000"
	personaBVN       = "95888168924"
)

func main() {
	baseURL := flag.String("base-url", "https://embedded-dev.bmoni.com", "BMONI base URL (origin only)")
	flag.Parse()

	apiKey := os.Getenv("BMONI_API_KEY")
	if apiKey == "" {
		fatal("BMONI_API_KEY is not set")
	}

	dsn := dsnFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("db connect: %v", err)
	}
	defer pool.Close()

	client := bmoni.NewClient(*baseURL, apiKey)

	// Stage 1/2: user + smart wallet.
	already := &models.BmoniWalletModel{DB: pool}
	existing, err := already.GetPlatform(ctx)
	if err == nil && existing.BmoniUserID != "" {
		fmt.Printf("Platform wallet already provisioned (user=%s, vba=%s)\n",
			existing.BmoniUserID, existing.VBAAccountNumber)
		return
	}

	userID, err := client.CreateUser(ctx, bmoni.CreateUserRequest{
		FirstName:   personaFirstName,
		LastName:    personaLastName,
		Email:       personaEmail,
		PhoneNumber: personaPhone,
		BVN:         personaBVN,
	})
	if err != nil {
		fatal("create user: %v", err)
	}
	fmt.Printf("BMONI user created: %s\n", userID)

	// Stage 2: KYC profile first (the persona name must be saved before the
	// rail verifies it).
	if err := client.SubmitKYC(ctx, userID, bmoni.KYCProfile{
		FirstName:   personaFirstName,
		LastName:    personaLastName,
		DateOfBirth: "1990-01-15",
		Gender:      "male",
		StreetLine1: "15 Admiralty Way",
		City:        "Lagos",
		State:       "Lagos",
		PostalCode:  "101233",
		CountryCode: "NGA",
	}); err != nil {
		fatal("submit kyc: %v", err)
	}
	fmt.Printf("KYC profile submitted\n")

	// Stage 3: owner key, then smart wallet (challenge scoped to the address).
	owner, err := bmoni.NewOwnerKey()
	if err != nil {
		fatal("generate owner key: %v", err)
	}

	smartWalletID, address, err := client.CreateWallet(ctx, userID, owner)
	if err != nil {
		fatal("create wallet: %v", err)
	}
	fmt.Printf("Smart wallet: %s (%s)\n", smartWalletID, address)

	// Stage 4: NGN rail. BVN lookup confirms the persona resolves (fetch-only);
	// start-nigeria provisions the VBA against the saved profile.
	if err := client.BVNLookup(ctx, userID, personaBVN); err != nil {
		fatal("bvn lookup: %v", err)
	}
	fmt.Printf("BVN resolved (%s)\n", personaBVN)
	if err := client.StartNigeria(ctx, userID, personaBVN, address); err != nil {
		fatal("start-nigeria: %v", err)
	}
	fmt.Printf("NGN rail started\n")

	// Stage 5: read the provisioned VBA (may lag; poll briefly).
	account, err := waitForVBA(ctx, client, userID, 15)
	if err != nil {
		fatal("deposit account: %v", err)
	}

	wallet := &models.BmoniWallet{
		BmoniUserID:      userID,
		OwnerAddress:     address,
		SmartWalletID:    smartWalletID,
		Currency:         "CNGN",
		Status:           "ACTIVE",
		VBAAccountNumber: account.AccountNumber,
		VBABankName:      account.BankName,
	}

	if err := already.Save(ctx, wallet); err != nil {
		fatal("save wallet: %v", err)
	}

	fmt.Printf("Platform wallet ready — VBA %s (%s)\n", account.AccountNumber, account.BankName)
	fmt.Printf("Owner address: %s (STORE the private key securely)\n", address)
}

func dsnFromEnv() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_HOST"), os.Getenv("DB_PORT"),
		os.Getenv("DB_NAME"), os.Getenv("DB_SSLMODE"),
	)
}

// waitForVBA polls DepositAccount until BMONI provisions the NGN number.
func waitForVBA(ctx context.Context, c *bmoni.Client, userID string, attempts int) (bmoni.DepositAccount, error) {
	var last bmoni.DepositAccount
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
	return last, fmt.Errorf("VBA not provisioned after %d attempts", attempts)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "bmoni-bootstrap: "+format+"\n", args...)
	os.Exit(1)
}
