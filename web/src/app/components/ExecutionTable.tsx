import { Fragment, useState } from 'react'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import { executionLedClass, type Execution } from '../../shared/types'
import { Badge, StatusBadge } from './Badge'

export function ExecutionTable({ executions }: { executions: Execution[] }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  const toggle = (id: string) => setExpanded(expanded === id ? null : id)
  // Cross-tenant rows only appear for the super admin; label the owning org.
  const showTenant = executions.some((e) => e.tenant)
  const colCount = showTenant ? 9 : 8
  return (
    <div className="table-wrap">
      <table className={`execution-table${showTenant ? ' cross-tenant' : ''}`}>
        <thead>
          <tr>
            <th>执行 / 任务</th>
            {showTenant && <th>组织</th>}
            <th>资源</th>
            <th>动作</th>
            <th>Agent</th>
            <th>风险</th>
            <th>状态</th>
            <th>开始时间</th>
            <th>耗时</th>
          </tr>
        </thead>
        <tbody>
          {executions.map((item) => (
            <Fragment key={item.id}>
              <tr onClick={() => toggle(item.id)} style={{ cursor: 'pointer' }} className={expanded === item.id ? 'row-selected' : ''}>
                <td>
                  <strong title={item.id}>{item.id}</strong>
                  <span className="cell-sub" title={item.taskId}>{item.taskId}</span>
                </td>
                {showTenant && <td className="mono">{item.tenant || '—'}</td>}
                <td>
                  <strong title={item.resource}>{item.resource}</strong>
                  <span className="cell-sub" title={item.parameters}>{item.parameters}</span>
                </td>
                <td title={item.action}>{item.action}</td>
                <td title={item.actor}>{item.actor}</td>
                <td>
                  <Badge tone={item.risk === '高' ? 'danger' : item.risk === '中' ? 'warning' : 'neutral'}>{item.risk}</Badge>
                </td>
                <td>
                  <span className={`exec-led ${executionLedClass(item.statusKey)}`}>
                    <i aria-hidden="true" />
                    <StatusBadge statusKey={item.statusKey} label={item.status} />
                  </span>
                </td>
                <td>{item.createdAt}</td>
                <td>{item.duration}</td>
              </tr>
              {expanded === item.id && (
                <tr className="detail-row">
                  <td colSpan={colCount}>
                    {item.errorMessage ? (
                      <div className="exec-detail error">
                        <AlertTriangle size={16} />
                        <pre>{item.errorMessage}</pre>
                      </div>
                    ) : item.result ? (
                      <div className="exec-detail">
                        <CheckCircle2 size={16} />
                        <pre>{JSON.stringify(item.result, null, 2)}</pre>
                      </div>
                    ) : (
                      <div className="exec-detail muted">
                        <span>暂无结果</span>
                      </div>
                    )}
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}
