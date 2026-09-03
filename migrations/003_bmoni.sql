-- BMONI paid enrollment.

-- Optional course price in kobo (1/100 of a Naira). NULL = free course.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'courses' AND column_name = 'price_kobo'
    ) THEN
        ALTER TABLE courses ADD COLUMN price_kobo BIGINT;
    END IF;
END $$;

-- The platform-owned BMONI wallet (singleton). Students pay by bank transfer
-- to this account; the wallet collects all course fees.
CREATE TABLE IF NOT EXISTS bmoni_wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bmoni_user_id TEXT UNIQUE,
    owner_address TEXT,
    smart_wallet_id TEXT,
    currency TEXT NOT NULL DEFAULT 'CNGN',
    status TEXT NOT NULL DEFAULT 'PENDING',
    vba_account_number TEXT,
    vba_bank_name TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- One payment attempt per course/student. PENDING -> PAID on BMONI webhook.
CREATE TABLE IF NOT EXISTS payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    amount_kobo BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'PAID', 'FAILED', 'MANUAL')),
    reference TEXT NOT NULL,
    narration_hint TEXT NOT NULL,
    matched_event_id TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    paid_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_payments_student ON payments (student_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments (status);

-- Paid course access for a student. PENDING -> ACTIVE once payment clears.
-- Separate from the free `course_enrollments` relation.
CREATE TABLE IF NOT EXISTS course_access (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    payment_id UUID REFERENCES payments(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING', 'ACTIVE')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (student_id, course_id)
);

-- Idempotency ledger for BMONI webhooks. event_id PK gives replay-safe dedupe.
CREATE TABLE IF NOT EXISTS webhook_events (
    event_id TEXT PRIMARY KEY,
    event_type TEXT,
    payload JSONB,
    processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
