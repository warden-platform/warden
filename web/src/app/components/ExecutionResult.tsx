import { AlertTriangle, CheckCircle2, Clock3, Copy, Database, Loader2, XCircle } from 'lucide-react'
import type { ApiExecution } from '../../shared/api'
import { mapExecutionStatus } from '../../shared/types'

export function ExecutionResult({ execution, actionName, onClose }: { execution: ApiExecution; actionName: string; onClose: () => void }) {
  const terminal = ['SUCCEEDED', 'FAILED', 'CANCELLED', 'EXPIRED', 'UNKNOWN'].includes(execution.status)
  const succeeded = execution.status === 'SUCCEEDED'
  const unknown = execution.status === 'UNKNOWN'
  const result = execution.result ?? {}
  const logs = typeof result.logs === 'string' ? result.logs : ''
  const cloudLogs = isCloudLogAction(actionName) ? normalizeCloudLogs(result) : []
  return (
    <div className="execution-tracker">
      <div className={`execution-state ${succeeded ? 'success' : unknown ? 'warning' : terminal ? 'failure' : ''}`}>
        {succeeded ? (
          <CheckCircle2 size={28} />
        ) : unknown ? (
          <AlertTriangle size={28} />
        ) : terminal ? (
          <XCircle size={28} />
        ) : (
          <Loader2 size={28} className="spin" />
        )}
        <div>
          <strong>
            {succeeded
              ? '执行成功'
              : unknown
                ? '结果待人工核对'
                : terminal
                  ? '执行失败'
                  : execution.status === 'WAITING_APPROVAL'
                    ? '等待审批'
                    : '正在执行'}
          </strong>
          <span>{mapExecutionStatus(execution.status)}</span>
        </div>
      </div>
      <div className="execution-timeline">
        {['策略判断', '排队', 'Connector 执行', '完成'].map((item, index) => (
          <div className={succeeded || (!terminal && index < 3) ? 'active' : ''} key={item}>
            <span>{index + 1}</span>
            <strong>{item}</strong>
          </div>
        ))}
      </div>
      {logs ? (
        <section className="execution-output">
          <div>
            <strong>日志结果</strong>
            <CopyButton value={logs} />
          </div>
          <pre>{logs}</pre>
        </section>
      ) : succeeded && isCloudLogAction(actionName) ? (
        <CloudLogResult result={result} entries={cloudLogs} />
      ) : succeeded && actionName === 'database.table.rows.query' ? (
        <DatabaseRowsResult result={result} />
      ) : succeeded && isDatabaseQueryAction(actionName) ? (
        <DatabaseQueryResult result={result} />
      ) : succeeded && actionName === 'database.table.describe' ? (
        <DatabaseDescribeResult result={result} />
      ) : succeeded ? (
        <section className="result-grid">
          {actionName === 'deployment.update_image' && (
            <>
              <div>
                <span>原镜像</span>
                <code>{String(result.previousImage ?? '-')}</code>
              </div>
              <div>
                <span>新镜像</span>
                <code>{String(result.image ?? '-')}</code>
              </div>
            </>
          )}
          {Object.entries(result)
            .filter(([key]) => !['logs', 'previousImage', 'image'].includes(key))
            .map(([key, value]) => (
              <div key={key}>
                <span>{key}</span>
                <strong>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</strong>
              </div>
            ))}
        </section>
      ) : null}
      {unknown && Boolean(result._pam_safety) && (
        <section className="execution-output">
          <div>
            <strong>重新读取到的目标状态</strong>
            <CopyButton value={JSON.stringify(result._pam_safety, null, 2)} />
          </div>
          <pre>{JSON.stringify(result._pam_safety, null, 2)}</pre>
        </section>
      )}
      {terminal && !succeeded && (
        <div className="credential-note error-note">
          <AlertTriangle size={17} />
          <span>{execution.errorMessage || execution.errorCode || '执行未成功完成'}</span>
        </div>
      )}
      <div className="drawer-actions">
        <button className="secondary-button" onClick={onClose}>
          关闭
        </button>
        {!terminal && (
          <button className="quiet-button" onClick={onClose}>
            转到后台
          </button>
        )}
      </div>
    </div>
  )
}

function DatabaseRowsResult({ result }: { result: Record<string, unknown> }) {
  if (result.purged === true)
    return (
      <section className="cloud-log-empty">
        <Clock3 size={20} />
        <div>
          <strong>原始数据库结果已清理</strong>
          <span>PAM 仅保留执行审计；业务数据仍只保存在源数据库。</span>
        </div>
      </section>
    )
  const columns = Array.isArray(result.columns) ? result.columns.map(String) : []
  const rows = Array.isArray(result.rows)
    ? result.rows.filter((row): row is Record<string, unknown> => Boolean(row && typeof row === 'object'))
    : []
  const raw = JSON.stringify(result, null, 2)
  return (
    <section className="database-query-result">
      <header>
        <div>
          <Database size={17} />
          <span>
            <strong>
              {String(result.schema ?? '-')} · {String(result.table ?? '-')}
            </strong>
            <small>
              {rows.length} 行{result.hasMore === true ? ' · 还有更多结果' : ''}
              {Number(result.truncatedCells ?? 0) > 0 ? ` · ${String(result.truncatedCells)} 个单元格已截断` : ''}
            </small>
          </span>
        </div>
        <CopyButton value={raw} />
      </header>
      {rows.length === 0 ? (
        <div className="cloud-log-empty">
          <Clock3 size={20} />
          <div>
            <strong>没有匹配数据</strong>
            <span>可以调整结构化过滤条件后重试。</span>
          </div>
        </div>
      ) : (
        <div className="database-query-scroll">
          <table>
            <thead>
              <tr>
                {columns.map((column) => (
                  <th key={column}>{column}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => (
                <tr key={index}>
                  {columns.map((column) => (
                    <td key={column}>
                      <code>{formatDatabaseCell(row[column])}</code>
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function formatDatabaseCell(value: unknown) {
  if (value === null || value === undefined) return 'NULL'
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

function isDatabaseQueryAction(actionName: string) {
  return actionName === 'database.query.execute' || actionName === 'database.query_template.execute'
}

function DatabaseQueryResult({ result }: { result: Record<string, unknown> }) {
  if (result.purged === true)
    return (
      <section className="cloud-log-empty">
        <Clock3 size={20} />
        <div>
          <strong>原始数据库结果已清理</strong>
          <span>PAM 仅保留执行审计；业务数据仍只保存在源数据库。</span>
        </div>
      </section>
    )
  const columns = Array.isArray(result.columns) ? result.columns.map(String) : []
  const rows = Array.isArray(result.rows)
    ? result.rows.filter((row): row is Record<string, unknown> => Boolean(row && typeof row === 'object'))
    : []
  const raw = JSON.stringify(result, null, 2)
  const notes = [
    rows.length > 0 ? `${rows.length} 行` : '',
    result.hasMore === true ? '还有更多结果' : '',
    Number(result.maskedCells ?? 0) > 0 ? `${String(result.maskedCells)} 个单元格已脱敏` : '',
    Number(result.truncatedCells ?? 0) > 0 ? `${String(result.truncatedCells)} 个单元格已截断` : '',
    Number(result.estimatedRows ?? 0) > 0 ? `预估扫描 ${String(result.estimatedRows)} 行` : '',
  ]
    .filter(Boolean)
    .join(' · ')
  return (
    <section className="database-query-result">
      <header>
        <div>
          <Database size={17} />
          <span>
            <strong>查询结果</strong>
            <small>{notes || '0 行'}</small>
          </span>
        </div>
        <CopyButton value={raw} />
      </header>
      {rows.length === 0 ? (
        <div className="cloud-log-empty">
          <Clock3 size={20} />
          <div>
            <strong>没有匹配数据</strong>
            <span>可以调整受治理查询后重试。</span>
          </div>
        </div>
      ) : (
        <div className="database-query-scroll">
          <table>
            <thead>
              <tr>
                {columns.map((column) => (
                  <th key={column}>{column}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => (
                <tr key={index}>
                  {columns.map((column) => (
                    <td key={column}>
                      <code>{formatDatabaseCell(row[column])}</code>
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function DatabaseDescribeResult({ result }: { result: Record<string, unknown> }) {
  const columns = (Array.isArray(result.columns) ? result.columns : []).filter((column): column is Record<string, unknown> =>
    Boolean(column && typeof column === 'object'),
  )
  const indexes = (Array.isArray(result.indexes) ? result.indexes : []).filter((index): index is Record<string, unknown> =>
    Boolean(index && typeof index === 'object'),
  )
  const constraints = (Array.isArray(result.constraints) ? result.constraints : []).filter(
    (constraint): constraint is Record<string, unknown> => Boolean(constraint && typeof constraint === 'object'),
  )
  const raw = JSON.stringify(result, null, 2)
  const estimatedRows = Number(result.estimatedRows ?? 0)
  const rowStat =
    result.rowCountEstimated === true
      ? `约 ${estimatedRows.toLocaleString()} 行${result.statsUpdatedAt ? ` · 统计时间 ${formatLogTime(String(result.statsUpdatedAt))}` : ''}`
      : estimatedRows > 0
        ? `${estimatedRows.toLocaleString()} 行`
        : '行数未知'
  const indexLabels = (columnName: string) => {
    const labels: string[] = []
    const inColumns = (value: unknown) => Array.isArray(value) && value.map(String).includes(columnName)
    if (constraints.some((constraint) => constraint.type === 'PRIMARY KEY' && inColumns(constraint.columns))) labels.push('PK')
    else if (
      constraints.some((constraint) => constraint.type === 'UNIQUE' && inColumns(constraint.columns)) ||
      indexes.some((index) => index.unique === true && inColumns(index.columns))
    )
      labels.push('UQ')
    for (const index of indexes) {
      if (index.unique !== true && inColumns(index.columns)) labels.push(String(index.name ?? ''))
    }
    return labels.filter(Boolean)
  }
  return (
    <section className="database-query-result">
      <header>
        <div>
          <Database size={17} />
          <span>
            <strong>
              {String(result.schema ?? '-')} · {String(result.table ?? '-')}
            </strong>
            <small>
              {columns.length} 列 · {rowStat}
              {result.engine ? ` · ${String(result.engine)}` : ''}
            </small>
          </span>
        </div>
        <CopyButton value={raw} />
      </header>
      {columns.length === 0 ? (
        <div className="cloud-log-empty">
          <Clock3 size={20} />
          <div>
            <strong>没有表结构信息</strong>
            <span>当前账号可能没有该表的元数据权限。</span>
          </div>
        </div>
      ) : (
        <div className="database-query-scroll">
          <table>
            <thead>
              <tr>
                <th>字段</th>
                <th>类型</th>
                <th>可空</th>
                <th>索引</th>
                <th>默认值</th>
              </tr>
            </thead>
            <tbody>
              {columns.map((column) => (
                <tr key={String(column.name)}>
                  <td>
                    <code>{String(column.name)}</code>
                  </td>
                  <td>{String(column.dataType)}</td>
                  <td>{column.nullable === true ? '是' : '否'}</td>
                  <td>
                    {indexLabels(String(column.name)).map((label) => (
                      <code className="index-badge" key={label}>
                        {label}
                      </code>
                    ))}
                  </td>
                  <td>
                    <code>{column.default === '' || column.default === undefined ? '-' : String(column.default)}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function CloudLogResult({ result, entries }: { result: Record<string, unknown>; entries: Record<string, unknown>[] }) {
  if (result.purged === true)
    return (
      <section className="cloud-log-empty">
        <Clock3 size={20} />
        <div>
          <strong>原始日志结果已清理</strong>
          <span>PAM 只保留执行审计，长期日志仍在云厂商。</span>
        </div>
      </section>
    )
  const raw = JSON.stringify(result, null, 2)
  return (
    <section className="cloud-log-result">
      <header>
        <div>
          <Database size={17} />
          <span>
            <strong>日志结果</strong>
            <small>{entries.length} 条 · 原始结果将在 PAM 中保留 10 分钟</small>
          </span>
        </div>
        <CopyButton value={raw} />
      </header>
      {entries.length === 0 ? (
        <div className="cloud-log-empty">
          <Clock3 size={20} />
          <div>
            <strong>当前时间范围没有匹配日志</strong>
            <span>可以调整时间范围或查询条件后重试。</span>
          </div>
        </div>
      ) : (
        <div className="cloud-log-lines">
          {entries.map((entry, index) => {
            const timestamp = logValue(entry, ['timestamp', 'time', '__time__', 'host_time', 'collect_time'])
            const content = logValue(entry, ['content', 'message', '__raw__', 'log', 'raw']) || JSON.stringify(entry)
            return (
              <article key={`${timestamp}-${index}`}>
                <time>{formatLogTime(timestamp)}</time>
                <pre>{content}</pre>
              </article>
            )
          })}
        </div>
      )}
    </section>
  )
}

function CopyButton({ value }: { value: string }) {
  return (
    <button type="button" className="quiet-button" onClick={() => navigator.clipboard?.writeText(value)}>
      <Copy size={14} />
      复制
    </button>
  )
}

function normalizeCloudLogs(result: Record<string, unknown>): Record<string, unknown>[] {
  const payload = result.result ?? result
  if (Array.isArray(payload)) return payload.map(asLogRecord)
  if (!payload || typeof payload !== 'object') return []
  const object = payload as Record<string, unknown>
  for (const key of ['logs', 'data', 'results', 'contents']) {
    if (Array.isArray(object[key])) return (object[key] as unknown[]).map(asLogRecord)
  }
  return []
}

function asLogRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : { content: String(value ?? '') }
}

function logValue(entry: Record<string, unknown>, keys: string[]) {
  for (const key of keys) if (entry[key] !== undefined && entry[key] !== null) return String(entry[key])
  return ''
}

function formatLogTime(value: string) {
  if (!value) return '-'
  const numeric = Number(value)
  const epoch = Number.isFinite(numeric) ? (numeric < 10_000_000_000 ? numeric * 1000 : numeric) : Date.parse(value)
  return Number.isFinite(epoch) ? new Date(epoch).toLocaleString('zh-CN', { hour12: false }) : value
}

function isCloudLogAction(actionName: string) {
  return actionName === 'huawei.lts.logs.query' || actionName === 'aliyun.sls.logs.query'
}
