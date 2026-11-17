package postgres

import (
	"context"
	"errors"
	"strings"

	"gitee.com/pam-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AIRepo struct {
	pool *pgxpool.Pool
}

func NewAIRepo(pool *pgxpool.Pool) *AIRepo { return &AIRepo{pool: pool} }

func (r *AIRepo) CreateConversation(ctx context.Context, principalID, title string) (*domain.AIConversation, error) {
	title = strings.TrimSpace(title)
	if runes := []rune(title); len(runes) > 200 {
		title = string(runes[:200])
	}
	var conversation domain.AIConversation
	err := r.pool.QueryRow(ctx, `INSERT INTO ai_conversations(tenant_id, principal_id, title)
		VALUES($1,$2,$3) RETURNING id,tenant_id,principal_id,title,created_at,updated_at`,
		repositoryTenant(ctx), principalID, title).Scan(&conversation.ID, &conversation.TenantID,
		&conversation.PrincipalID, &conversation.Title, &conversation.CreatedAt, &conversation.UpdatedAt)
	return &conversation, err
}

func (r *AIRepo) GetConversation(ctx context.Context, id, principalID string) (*domain.AIConversation, error) {
	var conversation domain.AIConversation
	err := r.pool.QueryRow(ctx, `SELECT id,tenant_id,principal_id,title,created_at,updated_at
		FROM ai_conversations WHERE id=$1 AND tenant_id=$2 AND principal_id=$3`, id, repositoryTenant(ctx), principalID).
		Scan(&conversation.ID, &conversation.TenantID, &conversation.PrincipalID, &conversation.Title,
			&conversation.CreatedAt, &conversation.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &conversation, err
}

func (r *AIRepo) CreateRun(ctx context.Context, conversationID string) (*domain.AIRun, error) {
	run := &domain.AIRun{ConversationID: conversationID, Status: "RUNNING"}
	err := r.pool.QueryRow(ctx, `INSERT INTO ai_runs(conversation_id,status) VALUES($1,'RUNNING') RETURNING id`, conversationID).Scan(&run.ID)
	return run, err
}

func (r *AIRepo) FinishRun(ctx context.Context, runID, status, errorCode string) error {
	_, err := r.pool.Exec(ctx, `UPDATE ai_runs SET status=$2,error_code=NULLIF($3,''),completed_at=now() WHERE id=$1`, runID, status, errorCode)
	return err
}

func (r *AIRepo) AddMessage(ctx context.Context, conversationID, runID, role, content string) (*domain.AIMessage, error) {
	message := &domain.AIMessage{ConversationID: conversationID, RunID: runID, Role: role, Content: content}
	err := r.pool.QueryRow(ctx, `INSERT INTO ai_messages(conversation_id,run_id,role,content) VALUES($1,NULLIF($2,'')::uuid,$3,$4)
		RETURNING id,created_at`, conversationID, runID, role, content).Scan(&message.ID, &message.CreatedAt)
	if err == nil {
		_, err = r.pool.Exec(ctx, `UPDATE ai_conversations SET updated_at=now() WHERE id=$1`, conversationID)
	}
	return message, err
}

func (r *AIRepo) ListMessages(ctx context.Context, conversationID, principalID string, limit int) ([]domain.AIMessage, error) {
	if limit < 1 || limit > 100 {
		limit = 40
	}
	rows, err := r.pool.Query(ctx, `SELECT m.id,m.conversation_id,COALESCE(m.run_id::text,''),m.role,m.content,m.created_at
		FROM ai_messages m JOIN ai_conversations c ON c.id=m.conversation_id
		WHERE m.conversation_id=$1 AND c.tenant_id=$2 AND c.principal_id=$3
		ORDER BY m.created_at DESC,m.id DESC LIMIT $4`, conversationID, repositoryTenant(ctx), principalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]domain.AIMessage, 0)
	for rows.Next() {
		var message domain.AIMessage
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.RunID, &message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, rows.Err()
}

func (r *AIRepo) AddToolCall(ctx context.Context, call *domain.AIToolCall) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO ai_tool_calls(run_id,provider_call_id,tool_name,status,output)
		VALUES($1,NULLIF($2,''),$3,$4,$5)`, call.RunID, call.ProviderID, call.ToolName, call.Status, call.Output)
	return err
}
