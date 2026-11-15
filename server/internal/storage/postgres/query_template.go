package postgres

import (
	"context"
	"fmt"

	"gitee.com/warden-platform/warden/server/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryTemplateRepo persists governed query templates and column
// classifications. Templates are immutable once published; the control plane
// reads the canonical AST here and injects it into database.query_template.execute
// dispatch, so the Connector never resolves a template from Agent input.
type QueryTemplateRepo struct {
	pool *pgxpool.Pool
}

func NewQueryTemplateRepo(pool *pgxpool.Pool) *QueryTemplateRepo {
	return &QueryTemplateRepo{pool: pool}
}

const queryTemplateColumns = `id, integration_id, schema_resource_id, name, version, status,
	canonical_ast, parameter_schema, resource_bindings, COALESCE(result_schema,'{}'::jsonb),
	max_rows, max_result_bytes, timeout_seconds, cost_policy, sensitivity,
	COALESCE(owner,''), COALESCE(created_by,''), COALESCE(approved_by,''), published_at,
	template_hash, tenant_id, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanQueryTemplate(sc rowScanner) (*domain.QueryTemplate, error) {
	var t domain.QueryTemplate
	err := sc.Scan(&t.ID, &t.IntegrationID, &t.SchemaResourceID, &t.Name, &t.Version, &t.Status,
		&t.CanonicalAST, &t.ParameterSchema, &t.ResourceBindings, &t.ResultSchema,
		&t.MaxRows, &t.MaxResultBytes, &t.TimeoutSeconds, &t.CostPolicy, &t.Sensitivity,
		&t.Owner, &t.CreatedBy, &t.ApprovedBy, &t.PublishedAt,
		&t.TemplateHash, &t.TenantID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// GetPublished returns the immutable published template for (integration,
// identifier, version). New clients use the template UUID; the unique template
// name remains accepted for compatibility with early Agent clients that were
// released while the templateId field was incorrectly resolved as a name.
func (r *QueryTemplateRepo) GetPublished(ctx context.Context, integrationID, identifier string, version int) (*domain.QueryTemplate, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT ` + queryTemplateColumns + `
		FROM database_query_templates
		WHERE integration_id=$1 AND (id::text=$2 OR name=$2) AND version=$3
			AND status='PUBLISHED' AND tenant_id=$4
		ORDER BY CASE WHEN id::text=$2 THEN 0 ELSE 1 END LIMIT 1`
	return scanQueryTemplate(r.pool.QueryRow(ctx, query, integrationID, identifier, version, tenantID))
}

// Create inserts a new template row (DRAFT by default). The canonical AST and
// its hash are the caller's responsibility (computed from ImportQuerySQL).
func (r *QueryTemplateRepo) Create(ctx context.Context, t *domain.QueryTemplate) error {
	query := `INSERT INTO database_query_templates
		(integration_id, schema_resource_id, name, version, status,
		 canonical_ast, parameter_schema, resource_bindings, result_schema,
		 max_rows, max_result_bytes, timeout_seconds, cost_policy, sensitivity,
		 owner, created_by, template_hash, tenant_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id, created_at, updated_at`
	return r.pool.QueryRow(ctx, query,
		t.IntegrationID, t.SchemaResourceID, t.Name, t.Version, t.Status,
		t.CanonicalAST, t.ParameterSchema, t.ResourceBindings, t.ResultSchema,
		t.MaxRows, t.MaxResultBytes, t.TimeoutSeconds, t.CostPolicy, t.Sensitivity,
		nullIf(t.Owner), nullIf(t.CreatedBy), t.TemplateHash, t.TenantID).
		Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

// ListColumnClassifications returns every (schema, table, column) output policy
// for one integration, used to build the per-alias masking map injected into
// query/template dispatch.
func (r *QueryTemplateRepo) ListColumnClassifications(ctx context.Context, integrationID string) ([]domain.ColumnClassification, error) {
	tenantID := repositoryTenant(ctx)
	rows, err := r.pool.Query(ctx, `SELECT id, integration_id, schema_name, table_name, column_name,
		classification, output_policy, tenant_id, created_at, updated_at
		FROM database_column_classifications WHERE integration_id=$1 AND tenant_id=$2`, integrationID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ColumnClassification
	for rows.Next() {
		var c domain.ColumnClassification
		if err := rows.Scan(&c.ID, &c.IntegrationID, &c.SchemaName, &c.TableName, &c.ColumnName,
			&c.Classification, &c.OutputPolicy, &c.TenantID, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// TableResourceExists reports whether an ACTIVE database_table resource with the
// given external id (table/<schema>/<table>) exists under the integration. Used
// by SQL import to reject queries referencing tables Warden does not govern.
func (r *QueryTemplateRepo) TableResourceExists(ctx context.Context, integrationID, externalID string) (bool, error) {
	tenantID := repositoryTenant(ctx)
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM resources
		WHERE resource_type='database_table' AND integration_id=$1 AND external_id=$2
			AND status='ACTIVE' AND tenant_id=$3)`, integrationID, externalID, tenantID).Scan(&exists)
	return exists, err
}

// GetByID returns a template by primary key.
func (r *QueryTemplateRepo) GetByID(ctx context.Context, id string) (*domain.QueryTemplate, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT ` + queryTemplateColumns + ` FROM database_query_templates WHERE id=$1 AND tenant_id=$2`
	return scanQueryTemplate(r.pool.QueryRow(ctx, query, id, tenantID))
}

// NextVersion returns the next available version for a (integration, name)
// template family so re-importing a name produces a new immutable version
// rather than colliding on the UNIQUE(integration_id, name, version) key.
func (r *QueryTemplateRepo) NextVersion(ctx context.Context, integrationID, name string) (int, error) {
	var version int
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM database_query_templates
		WHERE integration_id=$1 AND name=$2`, integrationID, name).Scan(&version)
	return version, err
}

// List returns templates filtered by integration and/or status, newest version
// first within a name.
func (r *QueryTemplateRepo) List(ctx context.Context, integrationID, status string, limit, offset int) ([]domain.QueryTemplate, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT ` + queryTemplateColumns + ` FROM database_query_templates WHERE tenant_id=$1`
	args := []any{tenantID}
	if integrationID != "" {
		args = append(args, integrationID)
		query += fmt.Sprintf(" AND integration_id=$%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status=$%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY name ASC, version DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.QueryTemplate
	for rows.Next() {
		t, err := scanQueryTemplate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	return result, rows.Err()
}

const publishRequestColumns = `id, template_id, requested_by, status,
	COALESCE(approved_by,''), COALESCE(rejected_reason,''), requested_at, decided_at,
	tenant_id, created_at, updated_at`

func scanPublishRequest(sc rowScanner) (*domain.TemplatePublishRequest, error) {
	var p domain.TemplatePublishRequest
	err := sc.Scan(&p.ID, &p.TemplateID, &p.RequestedBy, &p.Status,
		&p.ApprovedBy, &p.RejectedReason, &p.RequestedAt, &p.DecidedAt,
		&p.TenantID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// CreatePublishRequest submits a DRAFT template for publish review, rejecting
// non-DRAFT templates and duplicate pending requests.
func (r *QueryTemplateRepo) CreatePublishRequest(ctx context.Context, templateID, requestedBy string) (*domain.TemplatePublishRequest, error) {
	tenantID := repositoryTenant(ctx)
	var status string
	err := r.pool.QueryRow(ctx, `SELECT status FROM database_query_templates WHERE id=$1 AND tenant_id=$2`, templateID, tenantID).Scan(&status)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("template %s not found", templateID)
		}
		return nil, err
	}
	if status != "DRAFT" {
		return nil, fmt.Errorf("template is %s, only DRAFT can be submitted for publish", status)
	}
	var pending bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM database_query_template_publish_requests
		WHERE template_id=$1 AND status='PENDING')`, templateID).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, fmt.Errorf("template already has a pending publish request")
	}
	query := `INSERT INTO database_query_template_publish_requests (template_id, requested_by, tenant_id)
		VALUES ($1,$2,$3) RETURNING ` + publishRequestColumns
	return scanPublishRequest(r.pool.QueryRow(ctx, query, templateID, requestedBy, tenantID))
}

func (r *QueryTemplateRepo) GetPublishRequest(ctx context.Context, id string) (*domain.TemplatePublishRequest, error) {
	query := `SELECT ` + publishRequestColumns + ` FROM database_query_template_publish_requests WHERE id=$1`
	return scanPublishRequest(r.pool.QueryRow(ctx, query, id))
}

func (r *QueryTemplateRepo) ListPublishRequests(ctx context.Context, status string, limit, offset int) ([]domain.TemplatePublishRequest, error) {
	tenantID := repositoryTenant(ctx)
	query := `SELECT ` + publishRequestColumns + ` FROM database_query_template_publish_requests WHERE tenant_id=$1`
	args := []any{tenantID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status=$%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY requested_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.TemplatePublishRequest
	for rows.Next() {
		p, err := scanPublishRequest(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, rows.Err()
}

// ApprovePublish atomically marks a pending request APPROVED and transitions its
// template DRAFT → PUBLISHED (immutable from here on). The template_id clause
// ties the request to the URL's template so an approver cannot approve a
// request for a different template.
func (r *QueryTemplateRepo) ApprovePublish(ctx context.Context, requestID, templateID, approvedBy string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var boundTemplate string
	err = tx.QueryRow(ctx, `UPDATE database_query_template_publish_requests
		SET status='APPROVED', approved_by=$2, decided_at=now(), updated_at=now()
		WHERE id=$1 AND template_id=$3 AND status='PENDING'
		RETURNING template_id`, requestID, nullIf(approvedBy), templateID).Scan(&boundTemplate)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("publish request %s is not pending for template %s", requestID, templateID)
		}
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE database_query_templates
		SET status='PUBLISHED', approved_by=$2, published_at=now(), updated_at=now()
		WHERE id=$1 AND status='DRAFT'`, boundTemplate, nullIf(approvedBy))
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("template %s is not DRAFT", boundTemplate)
	}
	return tx.Commit(ctx)
}

// RejectPublish marks a pending request REJECTED; the template stays DRAFT so it
// can be edited (a new version) and resubmitted.
func (r *QueryTemplateRepo) RejectPublish(ctx context.Context, requestID, templateID, reason string) error {
	command, err := r.pool.Exec(ctx, `UPDATE database_query_template_publish_requests
		SET status='REJECTED', rejected_reason=$2, decided_at=now(), updated_at=now()
		WHERE id=$1 AND template_id=$3 AND status='PENDING'`, requestID, nullIf(reason), templateID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("publish request %s is not pending for template %s", requestID, templateID)
	}
	return nil
}
