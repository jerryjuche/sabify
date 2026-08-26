-- QUIZ RETAKES
-- When a teacher grants a retake, a row is inserted here.
-- The student can then retake the quiz once. The row is
-- deleted after the retake submission is recorded.
CREATE TABLE quiz_retakes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (quiz_id, student_id)
);
