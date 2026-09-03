package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type QuizGenerationRequest struct {
	CourseID          string `json:"course_id"`
	Topic             string `json:"topic"`
	NumberOfQuestions int    `json:"number_of_questions"`
	Difficulty        string `json:"difficulty"`
	QuestionType      string `json:"question_type"`
}

type GeneratedQuestion struct {
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	CorrectAnswer int      `json:"correct_answer"`
	Explanation   string   `json:"explanation"`
}

type QuizGenerationResponse struct {
	Questions []GeneratedQuestion `json:"questions"`
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) GenerateQuiz(
	ctx context.Context,
	request QuizGenerationRequest,
) (*QuizGenerationResponse, error) {

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to encode quiz generation request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+"/api/v1/quiz/generate",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to AI service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorResponse struct {
			Error  string `json:"error"`
			Detail string `json:"detail"`
		}

		_ = json.NewDecoder(resp.Body).Decode(&errorResponse)

		message := errorResponse.Error

		if message == "" {
			message = errorResponse.Detail
		}

		if message == "" {
			message = "unknown error from AI service"
		}

		return nil, fmt.Errorf(
			"AI service returned status %d: %s",
			resp.StatusCode,
			message,
		)
	}

	var result QuizGenerationResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf(
			"failed to decode AI service response: %w",
			err,
		)
	}

	return &result, nil
}
