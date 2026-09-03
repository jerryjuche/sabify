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
	CreatedAt        time.Time
}

type BmoniWalletModel struct {
	DB *pgxpool.Pool
}

func (m *BmoniWalletModel) GetPlatform(ctx context.Context) (*BmoniWallet, error) {
	query := `
		SELECT id, bmoni_user_id, owner_address, smart_wallet_id, currency, status, vba_account_number, vba_bank_name, created_at
		FROM bmoni_wallets
		ORDER BY created_at ASC
		LIMIT 1
	`

	var w BmoniWallet
	err := m.DB.QueryRow(ctx, query).Scan(
		&w.ID, &w.BmoniUserID, &w.OwnerAddress, &w.SmartWalletID,
		&w.Currency, &w.Status, &w.VBAAccountNumber, &w.VBABankName, &w.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoRecord
	} else if err != nil {
		return nil, err
	}

	return &w, nil
}

func (m *BmoniWalletModel) Save(ctx context.Context, w *BmoniWallet) error {
	query := `
		INSERT INTO bmoni_wallets (bmoni_user_id, owner_address, smart_wallet_id, currency, status, vba_account_number, vba_bank_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bmoni_user_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			smart_wallet_id = EXCLUDED.smart_wallet_id,
			currency = EXCLUDED.currency,
			status = EXCLUDED.status,
			vba_account_number = EXCLUDED.vba_account_number,
			vba_bank_name = EXCLUDED.vba_bank_name
		RETURNING id, created_at
	`

	return m.DB.QueryRow(
		ctx, query,
		w.BmoniUserID, w.OwnerAddress, w.SmartWalletID,
		w.Currency, w.Status, w.VBAAccountNumber, w.VBABankName,
	).Scan(&w.ID, &w.CreatedAt)
}
