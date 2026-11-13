package postgres

import (
	"context"
	"testing"
)

func TestDisabledOutboxIsNoop(t *testing.T) {
	repo := &OutboxRepo{}
	if err := repo.PublishTx(context.Background(), nil, "event", "execution", "00000000-0000-0000-0000-000000000000", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Publish(context.Background(), "event", "execution", "00000000-0000-0000-0000-000000000000", map[string]any{}); err != nil {
		t.Fatal(err)
	}
}
