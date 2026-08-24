package models

import "github.com/jackc/pgx/v5/pgxpool"

type Models struct {
	Users       UserModel
	Courses     CourseModel
	Quizzes     QuizModel
	Questions   QuestionModel
	Materials   MaterialModel
	Submissions SubmissionModel
	StudyGroups StudyGroupModel
	Enrollments EnrollmentModel
}

func NewModels(db *pgxpool.Pool) Models {
	return Models{
		Users:       UserModel{DB: db},
		Courses:     CourseModel{DB: db},
		Quizzes:     QuizModel{DB: db},
		Questions:   QuestionModel{DB: db},
		Materials:   MaterialModel{DB: db},
		Submissions: SubmissionModel{DB: db},
		StudyGroups: StudyGroupModel{DB: db},
		Enrollments: EnrollmentModel{DB: db},
	}
}
