/*
	tools/bmoni-bootstrap
	=====================

	Provisions the platform-owned BMONI wallet: user -> smart wallet -> KYC ->
	NGN rail, then persists the result into the bmoni_wallets table.

	Usage (from the repo root):

	    BMONI_API_KEY=... BMONI_WALLET_ENCRYPTION_KEY=$(openssl rand -hex 32) \
	        go run ./tools/bmoni-bootstrap            # Bunch Dillon (default)
	    ... go run ./tools/bmoni-bootstrap -persona samson   # Samson Jabo

	Personas (from BMONI's sandbox-test-data docs):
	  * bunch  — Bunch Dillon  — phone +2348000000000, BVN 95888168924
	  * samson — Samson Jabo   — phone +2348000000001, BVN 22222222222

	Behavior:
	  * If the local bmoni_wallets row for this persona already exists with a
	    stored owner key, it is left untouched.
	  * If the BMONI user already exists (POST /v1/users -> 409) but no local
	    row does, the tool recovers the existing user + NGN smart wallet via
	    GET /v1/smart-wallets/by-phone and saves the local row — the
	    documented recovery path for a create that already succeeded.
	  * A freshly provisioned wallet (new BMONI user) is saved and becomes the
	    active platform wallet: any older rows without a stored owner key are
	    removed afterwards so the app cannot pick a withdrawal-locked wallet.

	Withdrawals need the owner key, which is sealed with AES-256-GCM using
	BMONI_WALLET_ENCRYPTION_KEY and stored in bmoni_wallets.owner_key_enc.
	A recovered wallet's original owner key is unrecoverable, so withdrawals
	stay unavailable for it — deposits and enrollment still work.

	This is a setup/ops tool. It is never exercised by the running app.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"sabify/internal/bmoni"
	"sabify/internal/models"
)

type persona struct {
	key       string
	firstName string
	lastName  string
	email     string
	phone     string
	bvn       string
}

var personas = map[string]persona{
	"bunch": {
		key:       "bunch",
		firstName: "Bunch",
		lastName:  "Dillon",
		email:     "platform@sabify.example",
		phone:     "+2348000000000",
		bvn:       "95888168924",
	},
	"samson": {
		key:       "samson",
		firstName: "Samson",
		lastName:  "Jabo",
		email:     "platform-samson@sabify.example",
		phone:     "+2348000000001",
		bvn:       "22222222222",
	},
}

func main() {
	baseURL := flag.String("base-url", "https://embedded-dev.bmoni.com", "BMONI base URL (origin only)")
	personaKey := flag.String("persona", "bunch", "sandbox persona: bunch (Bunch Dillon) or samson (Samson Jabo)")
	flag.Parse()

	persona, ok := personas[*personaKey]
	if !ok {
		fatal("unknown persona %q (want bunch or samson)", *personaKey)
	}

	apiKey := os.Getenv("BMONI_API_KEY")
	if apiKey == "" {
		fatal("BMONI_API_KEY is not set")
	}

	dsn := dsnFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("db connect: %v", err)
	}
	defer pool.Close()

	client := bmoni.NewClient(*baseURL, apiKey)

	encKey := os.Getenv("BMONI_WALLET_ENCRYPTION_KEY")
	if encKey == "" {
		fmt.Fprintln(os.Stderr, "warning: BMONI_WALLET_ENCRYPTION_KEY is not set — any newly created owner key will NOT be stored and teacher withdrawals will be unavailable.")
	}

	walletModel := &models.BmoniWalletModel{DB: pool}

	// If the active platform wallet already has a stored owner key, it can
	// sign withdrawals — nothing to do. A keyless wallet (e.g. recovered from
	// a run that predates key encryption) is replaced below by provisioning
	// fresh, so the app stops picking a withdrawal-locked wallet.
	if w, gerr := walletModel.GetPlatform(ctx); gerr == nil && w != nil && w.OwnerKeyEnc != "" {
		fmt.Printf("Platform wallet already provisioned (user=%s, vba=%s) with owner key — nothing to do.\n",
			w.BmoniUserID, w.VBAAccountNumber)
		return
	}

	// Stage 1: create the user — or recover it if an earlier run already did.
	userID, err := client.CreateUser(ctx, bmoni.CreateUserRequest{
		FirstName:   persona.firstName,
		LastName:    persona.lastName,
		Email:       persona.email,
		PhoneNumber: persona.phone,
		BVN:         persona.bvn,
	})
	recovered := false
	if err != nil {
		if !strings.Contains(err.Error(), "409") {
			fatal("create user: %v", err)
		}
		// A 409 means an earlier attempt landed. Recover the existing user
		// instead of retrying (BMONI's "Retries and duplicates" doc).
		fmt.Fprintf(os.Stderr, "note: user already exists on BMONI — recovering via by-phone\n")
		resolved, rerr := client.ResolveUserByPhone(ctx, persona.phone)
		if rerr != nil {
			fatal("recover existing user: %v", rerr)
		}
		userID = resolved.UserID
		if userID == "" {
			fatal("recover existing user: empty bmoniUserId")
		}
		recovered = true
	}
	fmt.Printf("BMONI user: %s\n", userID)

	var (
		smartWalletID string
		address       string
		owner         *bmoni.OwnerKey
	)

	if recovered {
		// Stage 2-4 are already done on BMONI's side for this user. Adopt the
		// existing NGN smart wallet if there is one.
		resolved, err := client.ResolveUserByPhone(ctx, persona.phone)
		if err != nil {
			fatal("list wallets: %v", err)
		}
		for _, w := range resolved.Wallets {
			if w.Currency == "NGN" || w.Currency == "CNGN" {
				smartWalletID = w.ID
				address = w.Address
				fmt.Printf("Recovered NGN smart wallet %s (%s)\n", smartWalletID, address)
				break
			}
		}
		if smartWalletID == "" {
			fatal("recovered user has no NGN smart wallet; run the tool once with a fresh database to provision one")
		}
	} else {
		// Fresh user: run the full provisioning chain.

		// Stage 2: KYC profile. Pull the persona's record via the fetch-only
		// BVN lookup and submit those values — the docs' recommended order,
		// which guarantees the name/DOB match at rail verification time.
		record, err := client.LookupBVN(ctx, userID, persona.bvn)
		if err != nil {
			fatal("bvn lookup: %v", err)
		}
		fmt.Printf("BVN resolved — %s %s (DOB %s)\n", record.FirstName, record.LastName, record.DateOfBirth)

		firstName := record.FirstName
		if firstName == "" {
			firstName = persona.firstName
		}
		lastName := record.LastName
		if lastName == "" {
			lastName = persona.lastName
		}
		dateOfBirth := record.DateOfBirth
		if dateOfBirth == "" {
			dateOfBirth = "1990-01-15"
		}
		gender := record.Gender
		if gender == "" {
			gender = "male"
		}
		street := record.ResidentialAddr
		if street == "" {
			street = "15 Admiralty Way"
		}
		state := record.StateOfResidence
		if state == "" {
			state = "Lagos"
		}

		if err := client.SubmitKYC(ctx, userID, bmoni.KYCProfile{
			FirstName:   firstName,
			LastName:    lastName,
			DateOfBirth: dateOfBirth,
			Gender:      gender,
			StreetLine1: street,
			City:        "Lagos",
			State:       state,
			PostalCode:  "101233",
			CountryCode: "NGA",
		}); err != nil {
			fatal("submit kyc: %v", err)
		}
		fmt.Printf("KYC profile submitted\n")

		// Stage 3: owner key, then smart wallet (challenge scoped to the address).
		owner, err = bmoni.NewOwnerKey()
		if err != nil {
			fatal("generate owner key: %v", err)
		}

		smartWalletID, address, err = client.CreateWallet(ctx, userID, owner)
		if err != nil {
			fatal("create wallet: %v", err)
		}
		fmt.Printf("Smart wallet: %s (%s)\n", smartWalletID, address)

		// Stage 4: NGN rail. start-nigeria provisions the VBA against the
		// saved profile + BVN.
		if err := client.StartNigeria(ctx, userID, persona.bvn, address); err != nil {
			fatal("start-nigeria: %v", err)
		}
		fmt.Printf("NGN rail started\n")
	}

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

	// Seal a fresh owner key with BMONI_WALLET_ENCRYPTION_KEY so withdrawal
	// proposals can be signed after this run. A recovered wallet has no fresh
	// key (the original one is unrecoverable), so it stays withdrawal-locked.
	if owner != nil && encKey != "" {
		ownerKeyEnc, err := bmoni.EncryptOwnerKey(owner.Bytes(), []byte(encKey))
		if err != nil {
			fatal("encrypt owner key: %v", err)
		}
		wallet.OwnerKeyEnc = ownerKeyEnc
	}

	if err := walletModel.Save(ctx, wallet); err != nil {
		fatal("save wallet: %v", err)
	}

	// A freshly provisioned wallet is the active platform wallet: drop older
	// rows without a stored key so GetPlatform cannot pick a locked wallet.
	if !recovered {
		removed, derr := pool.Exec(ctx,
			`DELETE FROM bmoni_wallets WHERE bmoni_user_id <> $1 AND owner_key_enc IS NULL`, userID)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "note: could not remove older keyless wallet rows: %v\n", derr)
		} else {
			fmt.Printf("Removed %d older keyless wallet row(s); fresh wallet is now the platform wallet.\n", removed.RowsAffected())
		}
	}

	fmt.Printf("Platform wallet ready — VBA %s (%s)\n", account.AccountNumber, account.BankName)
	fmt.Printf("Owner address: %s\n", address)
	if wallet.OwnerKeyEnc != "" {
		fmt.Println("Owner key encrypted at rest — withdrawals enabled.")
	} else if recovered {
		fmt.Fprintln(os.Stderr, "Recovered wallet — the original owner key is unrecoverable, so withdrawals remain unavailable. Deposits and enrollment work.")
	} else {
		fmt.Fprintln(os.Stderr, "Owner key NOT stored — withdrawals unavailable until re-run with BMONI_WALLET_ENCRYPTION_KEY.")
	}
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