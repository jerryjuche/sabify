package models

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrForbidden = errors.New("models: forbidden")

type Models struct {
	Users         UserModel
	Courses       CourseModel
	Quizzes       QuizModel
	Questions     QuestionModel
	Materials     MaterialModel
	Submissions   SubmissionModel
	StudyGroups   StudyGroupModel
	Enrollments   EnrollmentModel
	Retakes       RetakeModel
	CourseAccess  CourseAccessModel
	Payments      PaymentModel
	BmoniWallets  BmoniWalletModel
	WebhookEvents WebhookEventModel
}

func NewModels(db *pgxpool.Pool) Models {
	return Models{
		Users:         UserModel{DB: db},
		Courses:       CourseModel{DB: db},
		Quizzes:       QuizModel{DB: db},
		Questions:     QuestionModel{DB: db},
		Materials:     MaterialModel{DB: db},
		Submissions:   SubmissionModel{DB: db},
		StudyGroups:   StudyGroupModel{DB: db},
		Enrollments:   EnrollmentModel{DB: db},
		Retakes:       RetakeModel{DB: db},
		CourseAccess:  CourseAccessModel{DB: db},
		Payments:      PaymentModel{DB: db},
		BmoniWallets:  BmoniWalletModel{DB: db},
		WebhookEvents: WebhookEventModel{DB: db},
	}
}
