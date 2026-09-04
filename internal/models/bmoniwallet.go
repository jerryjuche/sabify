package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BmoniWallet struct {
	ID               string
	BmoniUserID      string
	OwnerAddress     string
	SmartWalletID    string
	Currency         string
	Status           string
	VBAAccountNumber string
	VBABankName      string
	// OwnerKeyEnc is the wallet owner private key sealed with AES-256-GCM
	// (BMONI_WALLET_ENCRYPTION_KEY). It is required to sign withdrawal
	// proposals; NULL means withdrawals are unavailable.
	OwnerKeyEnc string
	// UserID is the owning teacher's local user id. NULL rows are legacy
	// platform wallets (pre-per-teacher). New wallets are always teacher-owned.
	UserID *string
	// KYCStatus tracks the in-app KYC wizard:
	// not_started → user_created → profile_saved → documents_uploaded → rail_active | failed
	KYCStatus string
	KYCError  string
	// BVN submitted at the profile step; re-supplied to start-nigeria when
	// the NGN rail is provisioned after document uploads.
	BVN       string
	CreatedAt time.Time
}

const (
	KYCNotStarted      = "not_started"
	KYCUserCreated     = "user_created"
	KYCProfileSaved    = "profile_saved"
	KYCDocsUploaded    = "documents_uploaded"
	KYCRailActive      = "rail_active"
	KYCFailed          = "failed"
)

type BmoniWalletModel struct {
	DB *pgxpool.Pool
}

const bmoniWalletColumns = `id, bmoni_user_id, owner_address, smart_wallet_id, currency, status,
	vba_account_number, vba_bank_name, owner_key_enc, user_id, kyc_status, kyc_error, bvn, created_at`

func scanBmoniWallet(row pgx.Row) (*BmoniWallet, error) {
	var w BmoniWallet
	err := row.Scan(
		&w.ID, &w.BmoniUserID, &w.OwnerAddress, &w.SmartWalletID,
		&w.Currency, &w.Status, &w.VBAAccountNumber, &w.VBABankName,
		&w.OwnerKeyEnc, &w.UserID, &w.KYCStatus, &w.KYCError, &w.BVN, &w.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if w.KYCStatus == "" {
		w.KYCStatus = KYCNotStarted
	}
	return &w, nil
}

// GetPlatform returns the legacy platform wallet (a row with no owning
// teacher). Kept only so the pre-per-teacher flow (and the webhook fallback)
// still resolves; new wallets are teacher-owned via GetByUserID.
func (m *BmoniWalletModel) GetPlatform(ctx context.Context) (*BmoniWallet, error) {
	query := `
		SELECT ` + bmoniWalletColumns + `
		FROM bmoni_wallets
		WHERE user_id IS NULL
		ORDER BY (owner_key_enc IS NOT NULL) DESC, created_at ASC
		LIMIT 1
	`
	w, err := scanBmoniWallet(m.DB.QueryRow(ctx, query))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}
	return w, nil
}

// GetByUserID returns the wallet owned by a teacher (primary accessor).
func (m *BmoniWalletModel) GetByUserID(ctx context.Context, userID string) (*BmoniWallet, error) {
	query := `
		SELECT ` + bmoniWalletColumns + `
		FROM bmoni_wallets
		WHERE user_id = $1
		LIMIT 1
	`
	w, err := scanBmoniWallet(m.DB.QueryRow(ctx, query, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}
	return w, nil
}

// GetByBmoniUserID resolves a wallet from a BMONI deposit's userId — used by
// the webhook to attribute a deposit to the owning teacher.
func (m *BmoniWalletModel) GetByBmoniUserID(ctx context.Context, bmoniUserID string) (*BmoniWallet, error) {
	query := `
		SELECT ` + bmoniWalletColumns + `
		FROM bmoni_wallets
		WHERE bmoni_user_id = $1
		LIMIT 1
	`
	w, err := scanBmoniWallet(m.DB.QueryRow(ctx, query, bmoniUserID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}
	return w, nil
}

// SetKYCStatus advances a teacher's wallet through the wizard. An empty
// errorText clears kyc_error (success transition).
func (m *BmoniWalletModel) SetKYCStatus(ctx context.Context, userID, status, errorText string) error {
	query := `
		UPDATE bmoni_wallets
		SET kyc_status = $2, kyc_error = NULLIF($3, '')
		WHERE user_id = $1
	`
	result, err := m.DB.Exec(ctx, query, userID, status, errorText)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNoRecord
	}
	return nil
}

// Save inserts or updates a wallet row. Teacher-owned rows (user_id set) are
// unique per local teacher — a wizard retry may carry a fresh BMONI user id,
// so the conflict target is the teacher's user_id, never bmoni_user_id.
// Legacy platform rows (user_id NULL) stay unique per BMONI user, matching
// the original singleton semantics.
func (m *BmoniWalletModel) Save(ctx context.Context, w *BmoniWallet) error {
	if w.KYCStatus == "" {
		w.KYCStatus = KYCNotStarted
	}

	var query string
	if w.UserID == nil || *w.UserID == "" {
		// Legacy platform row: unique per BMONI user (pre-per-teacher).
		query = `
			INSERT INTO bmoni_wallets (bmoni_user_id, owner_address, smart_wallet_id, currency, status, vba_account_number, vba_bank_name, owner_key_enc, user_id, kyc_status, kyc_error, bvn)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (bmoni_user_id) DO UPDATE SET
				owner_address = EXCLUDED.owner_address,
				smart_wallet_id = EXCLUDED.smart_wallet_id,
				currency = EXCLUDED.currency,
				status = EXCLUDED.status,
				vba_account_number = EXCLUDED.vba_account_number,
				vba_bank_name = EXCLUDED.vba_bank_name,
				owner_key_enc = COALESCE(EXCLUDED.owner_key_enc, bmoni_wallets.owner_key_enc),
				kyc_status = EXCLUDED.kyc_status,
				kyc_error = EXCLUDED.kyc_error,
				bvn = EXCLUDED.bvn
			RETURNING id, created_at
		`
	} else {
		// Teacher-owned row: unique per local teacher. A wizard retry can
		// carry a fresh BMONI user id, so conflict on user_id wins.
		query = `
			INSERT INTO bmoni_wallets (bmoni_user_id, owner_address, smart_wallet_id, currency, status, vba_account_number, vba_bank_name, owner_key_enc, user_id, kyc_status, kyc_error, bvn)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (user_id) WHERE user_id IS NOT NULL DO UPDATE SET
				bmoni_user_id = EXCLUDED.bmoni_user_id,
				owner_address = EXCLUDED.owner_address,
				smart_wallet_id = EXCLUDED.smart_wallet_id,
				currency = EXCLUDED.currency,
				status = EXCLUDED.status,
				vba_account_number = EXCLUDED.vba_account_number,
				vba_bank_name = EXCLUDED.vba_bank_name,
				owner_key_enc = COALESCE(EXCLUDED.owner_key_enc, bmoni_wallets.owner_key_enc),
				kyc_status = EXCLUDED.kyc_status,
				kyc_error = EXCLUDED.kyc_error,
				bvn = EXCLUDED.bvn
			RETURNING id, created_at
		`
	}

	return m.DB.QueryRow(
		ctx, query,
		w.BmoniUserID, w.OwnerAddress, w.SmartWalletID,
		w.Currency, w.Status, w.VBAAccountNumber, w.VBABankName,
		w.OwnerKeyEnc, w.UserID, w.KYCStatus, w.KYCError, w.BVN,
	).Scan(&w.ID, &w.CreatedAt)
}
