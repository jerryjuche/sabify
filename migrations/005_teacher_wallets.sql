-- Per-teacher BMONI wallets.
--
-- bmoni_wallets previously held a single platform-owned wallet (user_id NULL).
-- Each teacher now owns her own wallet: she completes an in-app KYC wizard and
-- the resulting BMONI user + VBA are stored on her row. Rows with user_id NULL
-- remain as legacy platform wallets (the webhook still resolves them), but new
-- wallets are always teacher-owned.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'bmoni_wallets' AND column_name = 'user_id'
    ) THEN
        ALTER TABLE bmoni_wallets ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'bmoni_wallets' AND column_name = 'kyc_status'
    ) THEN
        ALTER TABLE bmoni_wallets ADD COLUMN kyc_status TEXT NOT NULL DEFAULT 'not_started';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'bmoni_wallets' AND column_name = 'kyc_error'
    ) THEN
        ALTER TABLE bmoni_wallets ADD COLUMN kyc_error TEXT;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'bmoni_wallets' AND column_name = 'bvn'
    ) THEN
        -- The BVN used at KYC time; re-supplied to start-nigeria when the
        -- rail is provisioned after document uploads.
        ALTER TABLE bmoni_wallets ADD COLUMN bvn TEXT;
    END IF;
END $$;

-- A teacher can only ever have one wallet row.
CREATE UNIQUE INDEX IF NOT EXISTS bmoni_wallets_user_uidx
    ON bmoni_wallets (user_id)
    WHERE user_id IS NOT NULL;
