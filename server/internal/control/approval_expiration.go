package control

import (
	"context"
	"log"
	"time"

	"gitee.com/pam-platform/warden/server/internal/domain"
)

// StartBackground starts bounded maintenance loops owned by the control-plane
// process. Notification delivery itself remains best-effort and never blocks an
// approval transaction.
func (h *Handler) StartBackground(ctx context.Context) {
	h.startDingTalkCardStream(ctx)
	if h.pool == nil || h.requests == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		maintenanceRuns := 0
		for {
			if err := h.expirePendingApprovals(ctx); err != nil && ctx.Err() == nil {
				log.Printf("expire pending approvals failed: %v", err)
			}
			if _, err := h.executions.ExpireQueuedExecutions(ctx); err != nil && ctx.Err() == nil {
				log.Printf("expire queued executions failed: %v", err)
			}
			if _, err := h.executions.ExpireDispatchLeases(ctx); err != nil && ctx.Err() == nil {
				log.Printf("expire dispatched executions failed: %v", err)
			}
			if _, err := h.executions.PurgeTransientLogResults(ctx, 10*time.Minute); err != nil && ctx.Err() == nil {
				log.Printf("purge transient log results failed: %v", err)
			}
			maintenanceRuns++
			if maintenanceRuns%120 == 1 {
				if _, err := h.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at<now()-interval '7 days'`); err != nil && ctx.Err() == nil {
					log.Printf("purge expired admin sessions failed: %v", err)
				}
				if _, err := h.pool.Exec(ctx, `DELETE FROM cluster_pairings WHERE status IN ('PENDING','EXPIRED') AND expires_at<now()-interval '7 days'`); err != nil && ctx.Err() == nil {
					log.Printf("purge expired pairings failed: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (h *Handler) expirePendingApprovals(ctx context.Context) error {
	requests, err := h.requests.ListExpiredPending(ctx, 100)
	if err != nil {
		return err
	}
	for i := range requests {
		request := requests[i]
		requestCtx := domain.WithTenantID(ctx, request.TenantID)
		tx, err := h.pool.Begin(requestCtx)
		if err != nil {
			return err
		}
		if err := h.requests.UpdateStatusTx(requestCtx, tx, request.ID, string(domain.RequestPending), string(domain.RequestExpired), "", nil); err != nil {
			_ = tx.Rollback(requestCtx)
			continue // An approver won the race; leave its decision intact.
		}
		if request.ExecutionID != "" {
			changed, err := h.executions.TransitionStatusTx(requestCtx, tx, request.ExecutionID, domain.ExecWaitingApproval,
				domain.ExecExpired, "APPROVAL_EXPIRED", "审批请求已过期")
			if err != nil || !changed {
				_ = tx.Rollback(requestCtx)
				continue
			}
		}
		expiredAudit := &domain.AuditEvent{
			Action: "access_request.expired", ActorType: "system", ResourceID: request.ResourceID,
			ExecutionID: request.ExecutionID, TargetID: request.ID, ActingProjectID: request.ActingProjectID, Summary: "审批请求已过期", Metadata: map[string]any{},
		}
		attachRequestAudit(expiredAudit, &request)
		if err := h.audit.CreateTx(requestCtx, tx, expiredAudit); err != nil {
			_ = tx.Rollback(requestCtx)
			return err
		}
		if err := tx.Commit(requestCtx); err != nil {
			return err
		}
		h.notifyApprovalAsync(requestCtx, request, "access_request.expired", "")
	}
	return nil
}
