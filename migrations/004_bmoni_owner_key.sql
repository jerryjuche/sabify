-- BMONI owner key (encrypted at rest).
--
-- The platform smart-wallet owner key signs withdrawal (offramp) proposals.
-- It must survive the bootstrap run, so it is sealed with AES-256-GCM using
-- BMONI_WALLET_ENCRYPTION_KEY and stored here. The column stays NULL when the
-- operator bootstraps without an encryption key — withdrawals are then
-- unavailable until the key is stored (see tools/bmoni-bootstrap).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'bmoni_wallets' AND column_name = 'owner_key_enc'
    ) THEN
        ALTER TABLE bmoni_wallets ADD COLUMN owner_key_enc TEXT;
    END IF;
END $$;