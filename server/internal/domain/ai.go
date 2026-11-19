package domain

import "time"

type AIConversation struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenantId"`
	PrincipalID string    `json:"principalId"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type AIMessage struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	RunID          string    `json:"runId,omitempty"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"createdAt"`
}

type AIRun struct {
	ID             string
	ConversationID string
	Status         string
	ErrorCode      string
}

type AIToolCall struct {
	RunID      string
	ProviderID string
	ToolName   string
	Status     string
	Output     string
}
