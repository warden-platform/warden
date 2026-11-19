import { useState, type Dispatch, type SetStateAction } from 'react'
import { Loader2, X } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { DATABASE_FILTER_OPERATORS, type ExecutionStarter } from './constants'
import { asRecord, waitForTerminalExecution } from './utils'
import { useApiQuery } from '../../lib/useApiQuery'

// 单表快捷查询构建器：读取表结构（列/索引/行数），
// 让用户勾选返回字段、配置筛选与排序。

type DatabaseFilterInputKind = 'text' | 'number' | 'boolean' | 'date' | 'time' | 'datetime-local'

function databaseFilterInputKind(column: Record<string, unknown> | undefined): DatabaseFilterInputKind {
  const dataType = String(column?.dataType ?? '')
    .trim()
    .toLowerCase()
  if (/^(bool|boolean)$/.test(dataType)) return 'boolean'
  if (/^(tinyint|smallint|mediumint|int|integer|bigint|decimal|numeric|float|double|real|bit|year)(\b|\()/.test(dataType)) return 'number'
  if (/^(datetime|timestamp)(\b|\()/.test(dataType)) return 'datetime-local'
  if (/^date(\b|\()/.test(dataType)) return 'date'
  if (/^time(\b|\()/.test(dataType)) return 'time'
  return 'text'
}

export function DatabaseFilterValueInput({
  column,
  operator,
  value,
  onChange,
}: {
  column: Record<string, unknown> | undefined
  operator: string
  value: unknown
  onChange: (value: unknown) => void
}) {
  if (operator === 'is_null' || operator === 'is_not_null') return null
  if (operator === 'in') {
    const rawValue = Array.isArray(value) ? value.map(String).join(', ') : String(value ?? '')
    return (
      <input
        type="text"
        value={rawValue}
        onChange={(event) =>
          onChange(
            event.target.value
              .split(',')
              .map((item) => item.trim())
              .filter(Boolean),
          )
        }
        placeholder="多个值用逗号分隔"
        aria-label="筛选值"
      />
    )
  }
  const inputKind = databaseFilterInputKind(column)
  if (inputKind === 'boolean') {
    return (
      <select
        value={String(value ?? '')}
        onChange={(event) => onChange(event.target.value === '' ? '' : event.target.value === 'true')}
        aria-label="筛选值"
      >
        <option value="">请选择</option>
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    )
  }
  return (
    <input
      type={inputKind}
      step={inputKind === 'number' ? 'any' : undefined}
      value={String(value ?? '')}
      onChange={(event) => onChange(event.target.value)}
      placeholder="值"
      aria-label="筛选值"
    />
  )
}

export function DatabaseTableQueryBuilder({
  resource,
  parameters,
  setParameters,
  startExecution,
}: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  startExecution: ExecutionStarter
}) {
  const [rawMetadata, setRawMetadata] = useState<Record<string, unknown> | null>(null)
  const [loadedFor, setLoadedFor] = useState('')

  const metadataQuery = useApiQuery(
    async () => {
      if (!resource) return {}
      const started = await startExecution({
        taskId: `ui-db-describe-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
        resourceId: resource.id,
        action: 'database.table.describe',
        actionVersion: 1,
        parameters: {
          name: resource.name,
          ...(resource.namespace ? { namespace: resource.namespace } : {}),
        },
        reason: '查询表单读取表结构、索引与行数',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '读取表结构失败')
      return completed.result ?? {}
    },
    [resource?.id, startExecution],
    {
      enabled: Boolean(resource && resource.id !== loadedFor),
      onSuccess: (data) => {
        setRawMetadata(data)
        if (resource) setLoadedFor(resource.id)
      },
    },
  )
  const loading = metadataQuery.loading
  const error = metadataQuery.error

  // Stale metadata (from a previous resource) is derived away, not reset in an effect.
  const metadata = resource && loadedFor === resource.id ? rawMetadata : null

  const columns = (Array.isArray(metadata?.columns) ? metadata.columns : []).filter((column): column is Record<string, unknown> =>
    Boolean(column && typeof column === 'object'),
  )
  const indexes = (Array.isArray(metadata?.indexes) ? metadata.indexes : []).filter((index): index is Record<string, unknown> =>
    Boolean(index && typeof index === 'object'),
  )
  const constraints = (Array.isArray(metadata?.constraints) ? metadata.constraints : []).filter(
    (constraint): constraint is Record<string, unknown> => Boolean(constraint && typeof constraint === 'object'),
  )
  const selectedColumns = Array.isArray(parameters.columns) ? parameters.columns.map(String) : []
  const filters = (Array.isArray(parameters.filters) ? parameters.filters : []).filter((filter): filter is Record<string, unknown> =>
    Boolean(filter && typeof filter === 'object'),
  )

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

  const toggleColumn = (name: string) =>
    setParameters((current) => {
      const currentColumns = Array.isArray(current.columns) ? current.columns.map(String) : []
      const next = currentColumns.includes(name) ? currentColumns.filter((column) => column !== name) : [...currentColumns, name]
      return { ...current, columns: next }
    })

  const addFilter = () =>
    setParameters((current) => ({
      ...current,
      filters: [
        ...(Array.isArray(current.filters) ? current.filters : []),
        { column: String(columns[0]?.name ?? ''), operator: 'eq', value: '' },
      ],
    }))

  const updateFilter = (index: number, patch: Record<string, unknown>) =>
    setParameters((current) => {
      const currentFilters = (Array.isArray(current.filters) ? current.filters : []).map((filter) => ({ ...asRecord(filter) }))
      currentFilters[index] = { ...currentFilters[index], ...patch }
      return { ...current, filters: currentFilters }
    })

  const removeFilter = (index: number) =>
    setParameters((current) => ({
      ...current,
      filters: (Array.isArray(current.filters) ? current.filters : []).filter((_, filterIndex) => filterIndex !== index),
    }))

  const orders = (Array.isArray(parameters.orderBy) ? parameters.orderBy : []).filter((order): order is Record<string, unknown> =>
    Boolean(order && typeof order === 'object'),
  )
  const addOrder = () =>
    setParameters((current) => ({
      ...current,
      orderBy: [...(Array.isArray(current.orderBy) ? current.orderBy : []), { column: String(columns[0]?.name ?? ''), direction: 'asc' }],
    }))
  const updateOrder = (index: number, patch: Record<string, unknown>) =>
    setParameters((current) => {
      const currentOrders = (Array.isArray(current.orderBy) ? current.orderBy : []).map((order) => ({ ...asRecord(order) }))
      currentOrders[index] = { ...currentOrders[index], ...patch }
      return { ...current, orderBy: currentOrders }
    })
  const removeOrder = (index: number) =>
    setParameters((current) => ({
      ...current,
      orderBy: (Array.isArray(current.orderBy) ? current.orderBy : []).filter((_, orderIndex) => orderIndex !== index),
    }))

  const rowStat = Number(metadata?.estimatedRows ?? 0)
  return (
    <fieldset className="database-query-builder">
      <legend>返回字段与筛选（单表快捷查询）</legend>
      {!resource ? (
        <div className="form-error" role="alert">
          请先选择数据库表资源。
        </div>
      ) : loading ? (
        <div className="onboarding-hint">
          <Loader2 size={16} className="spin" />
          <span>正在读取表结构、索引与行数…</span>
        </div>
      ) : error ? (
        <div className="form-error" role="alert">
          {error}
          <button type="button" className="quiet-button" onClick={() => metadataQuery.reload()}>
            重试
          </button>
        </div>
      ) : metadata ? (
        <>
          <div className="database-builder-meta">
            {columns.length} 列 · {rowStat > 0 ? `约 ${rowStat.toLocaleString()} 行` : '行数未知'}
            {metadata.statsUpdatedAt ? ` · 统计时间 ${String(metadata.statsUpdatedAt)}` : ''}
          </div>
          <div className="database-builder-selection-head">
            <div>
              <strong>返回字段</strong>
              <small>
                已选择 {selectedColumns.length} / {columns.length} 个字段，可多选
              </small>
            </div>
          </div>
          <div className="database-builder-columns">
            {columns.map((column) => {
              const name = String(column.name)
              return (
                <label key={name} className={selectedColumns.includes(name) ? 'selected' : ''}>
                  <input type="checkbox" checked={selectedColumns.includes(name)} onChange={() => toggleColumn(name)} />
                  <span className="database-column-details">
                    <strong>{name}</strong>
                    <small>
                      {String(column.dataType)}
                      {column.nullable === true ? ' · 可空' : ''}
                    </small>
                  </span>
                  {indexLabels(name).map((label) => (
                    <code className="index-badge" key={label}>
                      {label}
                    </code>
                  ))}
                </label>
              )
            })}
          </div>
          <div className="database-builder-filters">
            <div className="database-builder-filter-head">
              <strong>筛选条件</strong>
              <button type="button" className="quiet-button" onClick={addFilter}>
                添加筛选
              </button>
            </div>
            {filters.length === 0 ? (
              <div className="onboarding-hint">未添加筛选，将按 limit 返回表数据。</div>
            ) : (
              filters.map((filter, index) => {
                const item = asRecord(filter)
                const operator = String(item.operator ?? 'eq')
                const selectedColumn = columns.find((column) => String(column.name) === String(item.column ?? ''))
                return (
                  <div className="database-builder-filter-row" key={index}>
                    <select value={String(item.column ?? '')} onChange={(event) => updateFilter(index, { column: event.target.value })}>
                      {columns.map((column) => (
                        <option key={String(column.name)} value={String(column.name)}>
                          {String(column.name)}
                        </option>
                      ))}
                    </select>
                    <select value={operator} onChange={(event) => updateFilter(index, { operator: event.target.value })}>
                      {DATABASE_FILTER_OPERATORS.map((value) => (
                        <option key={value} value={value}>
                          {value}
                        </option>
                      ))}
                    </select>
                    <DatabaseFilterValueInput
                      column={selectedColumn}
                      operator={operator}
                      value={item.value}
                      onChange={(value) => updateFilter(index, { value })}
                    />
                    <button type="button" className="icon-button" onClick={() => removeFilter(index)} aria-label="删除筛选">
                      <X size={14} />
                    </button>
                  </div>
                )
              })
            )}
          </div>
          <div className="database-builder-filters">
            <div className="database-builder-filter-head">
              <strong>排序</strong>
              <button type="button" className="quiet-button" onClick={addOrder}>
                添加排序
              </button>
            </div>
            {orders.length === 0 ? (
              <div className="onboarding-hint">未添加排序，结果顺序由数据库决定。</div>
            ) : (
              orders.map((order, index) => {
                const item = asRecord(order)
                return (
                  <div className="database-builder-filter-row" key={index}>
                    <select value={String(item.column ?? '')} onChange={(event) => updateOrder(index, { column: event.target.value })}>
                      {columns.map((column) => (
                        <option key={String(column.name)} value={String(column.name)}>
                          {String(column.name)}
                        </option>
                      ))}
                    </select>
                    <select
                      value={String(item.direction ?? 'asc')}
                      onChange={(event) => updateOrder(index, { direction: event.target.value })}
                    >
                      <option value="asc">asc</option>
                      <option value="desc">desc</option>
                    </select>
                    <button type="button" className="icon-button" onClick={() => removeOrder(index)} aria-label="删除排序">
                      <X size={14} />
                    </button>
                  </div>
                )
              })
            )}
          </div>
        </>
      ) : null}
    </fieldset>
  )
}
