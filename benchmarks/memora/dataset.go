package main

import (
	"encoding/json"
	"os"
)

// Dataset represents the Memora benchmark dataset
type Dataset struct {
	Config struct {
		Version     string `json:"version"`
		DatasetType string `json:"dataset_type"`
		NumUsers    int    `json:"num_users"`
		Description string `json:"description"`
	} `json:"config"`
	Users     []User     `json:"users"`
	Questions []Question `json:"questions"`
}

// User represents a user in the dataset
type User struct {
	UserID   string    `json:"user_id"`
	Sessions []Session `json:"sessions"`
}

// Session represents a session for a user
type Session struct {
	SessionID string `json:"session_id"`
	Date      string `json:"date"`
	Turns     []Turn `json:"turns"`
}

// Turn represents a turn in a conversation
type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Question represents an evaluation question
type Question struct {
	QID              string `json:"q_id"`
	QuestionType     string `json:"question_type"` // remembering|reasoning|recommending
	Question         string `json:"question"`
	Answer           string `json:"answer"`
	InvalidatedAnswer string `json:"invalidated_answer"` // What would be wrong/stale
	UserID           string `json:"user_id"`
}

// LoadDataset loads a dataset from a JSON file
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var dset Dataset
	if err := json.Unmarshal(data, &dset); err != nil {
		return nil, err
	}

	return &dset, nil
}
