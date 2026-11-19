import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react'
import { Loader2, ShieldCheck, X } from 'lucide-react'
import type { ApiResource } from '../../../shared/api'
import { listResources } from '../../../shared/api'
import { DATABASE_FILTER_OPERATORS, DATABASE_QUERY_AST_VERSION, DATABASE_QUERY_SOURCE_ALIAS, type ExecutionStarter } from './constants'
import { asRecord, normalizedDatabaseQueryAst, waitForTerminalExecution } from './utils'
import { DatabaseFilterValueInput } from './DatabaseTableQueryBuilder'
import { EMPTY_ARRAY, useApiQuery } from '../../lib/useApiQuery'

// 多表 JOIN 查询构造器（AST 模式）：管理 sources/joins/select/where/bindings，
// 提交前需先运行预检生成 previewHash（成本门禁绑定）。

type DatabaseQueryBuilderField = {
  key: string
  source: string
  column: string
  table: ApiResource
  metadata: Record<string, unknown>
  definition: Record<string, unknown>
}

const queryFieldKey = (source: string, column: string) => `${source}.${column}`

function queryFieldRef(key: string) {
  const separator = key.indexOf('.')
  return separator > 0
    ? { source: key.slice(0, separator), column: key.slice(separator + 1) }
    : { source: DATABASE_QUERY_SOURCE_ALIAS, column: key }
}

function queryOutputAlias(source: string, column: string) {
  const raw = source === DATABASE_QUERY_SOURCE_ALIAS ? column : `${source}_${column}`
  return raw.slice(0, 64)
}

export function DatabaseQueryAstBuilder({
  resource,
  parameters,
  setParameters,
  previewRequest,
  onPreviewLoadingChange,
  onPreviewVerdictChange,
  startExecution,
  actingProjectId,
}: {
  resource: ApiResource | null
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  previewRequest: number
  onPreviewLoadingChange: (loading: boolean) => void
  onPreviewVerdictChange: (verdict: string) => void
  startExecution: ExecutionStarter
  actingProjectId: string
}) {
  const [metadataByExternalId, setMetadataByExternalId] = useState<Record<string, Record<string, unknown>>>({})
  const [manualJoinError, setManualJoinError] = useState('')
  const [joinTableId, setJoinTableId] = useState('')
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const metadataRequests = useRef(new Set<string>())
  const handledPreviewRequest = useRef(previewRequest)

  const invalidatePreview = () => {
    setPreview(null)
    setPreviewError('')
    onPreviewVerdictChange('')
  }

  const describeTable = useCallback(
    async (table: ApiResource) => {
      const started = await startExecution({
        taskId: `ui-db-describe-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
        resourceId: table.id,
        action: 'database.table.describe',
        actionVersion: 1,
        parameters: { name: table.name, ...(table.namespace ? { namespace: table.namespace } : {}) },
        reason: '多表查询构造器读取表结构、索引与行数',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED')
        throw new Error(completed.errorMessage || completed.errorCode || `读取表 ${table.name} 结构失败`)
      const metadata = completed.result ?? {}
      setMetadataByExternalId((current) => ({ ...current, [table.external_id]: metadata }))
      return metadata
    },
    [startExecution],
  )

  const ensureTableMetadata = useCallback(
    async (table: ApiResource) => {
      if (metadataByExternalId[table.external_id]) return metadataByExternalId[table.external_id]
      if (metadataRequests.current.has(table.external_id)) {
        for (let attempt = 0; attempt < 50; attempt += 1) {
          await new Promise((resolve) => window.setTimeout(resolve, 100))
          const existing = metadataByExternalId[table.external_id]
          if (existing) return existing
        }
      }
      metadataRequests.current.add(table.external_id)
      try {
        return await describeTable(table)
      } finally {
        metadataRequests.current.delete(table.external_id)
      }
    },
    [describeTable, metadataByExternalId],
  )

  const baseMetadataQuery = useApiQuery(
    () => describeTable(resource!),
    [resource?.id, describeTable],
    {
      enabled: Boolean(resource && !metadataByExternalId[resource.external_id]),
      onSuccess: (data) => {
        if (resource) setMetadataByExternalId((prev) => ({ ...prev, [resource.external_id]: data }))
      },
    },
  )
  const loading = baseMetadataQuery.loading
  const error = baseMetadataQuery.error

  const tablesQuery = useApiQuery(
    async () => {
      if (!resource?.integration_id) return [] as ApiResource[]
      const items = await listResources({ type: 'database_table', integrationId: resource.integration_id, projectId: actingProjectId })
      // Keep the client-side guard even though the catalog endpoint is
      // filtered. It prevents a stale proxy/cache response from surfacing a
      // schema, instance, or table from another Integration as a JOIN target.
      const available = (items ?? []).filter(
        (item) =>
          item.resource_type === 'database_table' &&
          item.status === 'ACTIVE' &&
          item.connector_id === resource.connector_id &&
          item.integration_id === resource.integration_id,
      )
      return available.some((item) => item.id === resource.id) ? available : [resource, ...available]
    },
    [resource?.id, resource?.integration_id, resource?.connector_id, actingProjectId],
    { enabled: Boolean(resource?.integration_id) },
  )
  const tableResources = resource?.integration_id ? (tablesQuery.data ?? EMPTY_ARRAY) : resource ? [resource] : EMPTY_ARRAY
  const tableLoading = tablesQuery.loading
  const joinError = manualJoinError || tablesQuery.error

  const ast = asRecord(parameters.ast)
  useEffect(() => {
    if (!resource) return
    const normalized = normalizedDatabaseQueryAst(ast, resource)
    const sources = Array.isArray(ast.sources) ? ast.sources : []
    const pinned = sources.length === 1 && asRecord(sources[0]).resourceId === resource.external_id
    if (
      ast.astVersion === DATABASE_QUERY_AST_VERSION &&
      pinned &&
      Array.isArray(ast.select) &&
      ast.bindings &&
      typeof ast.bindings === 'object'
    )
      return
    setParameters((current) => ({ ...current, ast: normalized }))
  }, [resource, ast, setParameters])

  const joins = (Array.isArray(ast.joins) ? ast.joins : []).filter((join): join is Record<string, unknown> =>
    Boolean(join && typeof join === 'object'),
  )
  const sourceEntries: Array<{ alias: string; table: ApiResource; metadata?: Record<string, unknown> }> = []
  if (resource) {
    sourceEntries.push({ alias: DATABASE_QUERY_SOURCE_ALIAS, table: resource, metadata: metadataByExternalId[resource.external_id] })
    for (const join of joins) {
      const source = asRecord(join.source)
      const table = tableResources.find((item) => item.external_id === String(source.resourceId ?? ''))
      if (table) sourceEntries.push({ alias: String(source.alias ?? ''), table, metadata: metadataByExternalId[table.external_id] })
    }
  }

  useEffect(() => {
    for (const entry of sourceEntries.slice(1)) {
      if (!entry.metadata && !metadataRequests.current.has(entry.table.external_id))
        void ensureTableMetadata(entry.table).catch((err: Error) => setManualJoinError(err.message))
    }
  }, [sourceEntries.map((entry) => `${entry.alias}:${entry.table.id}:${Boolean(entry.metadata)}`).join('|'), ensureTableMetadata]) // eslint-disable-line react-hooks/exhaustive-deps

  const fieldOptions: DatabaseQueryBuilderField[] = sourceEntries.flatMap((entry) => {
    const columns = (Array.isArray(entry.metadata?.columns) ? entry.metadata.columns : []).filter(
      (column): column is Record<string, unknown> => Boolean(column && typeof column === 'object'),
    )
    return columns.map((column) => ({
      key: queryFieldKey(entry.alias, String(column.name)),
      source: entry.alias,
      column: String(column.name),
      table: entry.table,
      metadata: entry.metadata ?? {},
      definition: column,
    }))
  })
  const fieldByKey = new Map(fieldOptions.map((field) => [field.key, field]))
  const selects = (Array.isArray(ast.select) ? ast.select : []).filter((select): select is Record<string, unknown> =>
    Boolean(select && typeof select === 'object'),
  )
  const selectedKeys = selects
    .map((select) => {
      const field = asRecord(select.field)
      return queryFieldKey(String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS), String(field.column ?? ''))
    })
    .filter((key) => fieldByKey.has(key))
  const selectedAliases = selects.map((select) => String(select.alias ?? '')).filter(Boolean)
  const where = asRecord(ast.where)
  const bindings = asRecord(ast.bindings)
  const filters = (Array.isArray(where.and) ? where.and : [])
    .filter((filter): filter is Record<string, unknown> => Boolean(filter && typeof filter === 'object'))
    .map((filter) => {
      const item = asRecord(filter)
      const field = asRecord(item.field)
      const parameter = String(item.parameter ?? '')
      return {
        source: String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS),
        column: String(field.column ?? ''),
        operator: String(item.operator ?? 'eq'),
        value: parameter ? bindings[parameter] : item.value,
      }
    })
  const orders = (Array.isArray(ast.orderBy) ? ast.orderBy : []).filter((order): order is Record<string, unknown> =>
    Boolean(order && typeof order === 'object'),
  )
  const limit = typeof ast.limit === 'number' ? Number(ast.limit) : 50
  const baseMetadata = resource ? metadataByExternalId[resource.external_id] : undefined

  const updateAst = (patch: Record<string, unknown>) => {
    invalidatePreview()
    setParameters((current) => {
      if (!resource) return current
      const next: Record<string, unknown> = { ...current, ast: { ...normalizedDatabaseQueryAst(current.ast, resource), ...patch } }
      delete next.previewHash
      return next
    })
  }

  const encodeFilters = (items: Record<string, unknown>[]) => {
    const leaves: Record<string, unknown>[] = []
    const nextBindings: Record<string, unknown> = {}
    items.forEach((item, index) => {
      const operator = String(item.operator ?? 'eq')
      const leaf: Record<string, unknown> = {
        field: { source: String(item.source ?? DATABASE_QUERY_SOURCE_ALIAS), column: String(item.column ?? '') },
        operator,
      }
      if (operator !== 'is_null' && operator !== 'is_not_null') {
        const parameter = `p${index + 1}`
        leaf.parameter = parameter
        nextBindings[parameter] = item.value
      }
      leaves.push(leaf)
    })
    return { where: leaves.length > 0 ? { and: leaves } : undefined, bindings: nextBindings }
  }
  const writeFilters = (items: Record<string, unknown>[]) => updateAst(encodeFilters(items))

  const indexLabels = (metadata: Record<string, unknown>, columnName: string) => {
    const indexes = (Array.isArray(metadata.indexes) ? metadata.indexes : []).filter((item): item is Record<string, unknown> =>
      Boolean(item && typeof item === 'object'),
    )
    const constraints = (Array.isArray(metadata.constraints) ? metadata.constraints : []).filter((item): item is Record<string, unknown> =>
      Boolean(item && typeof item === 'object'),
    )
    const labels: string[] = []
    const inColumns = (value: unknown) => Array.isArray(value) && value.map(String).includes(columnName)
    if (constraints.some((constraint) => constraint.type === 'PRIMARY KEY' && inColumns(constraint.columns))) labels.push('PK')
    else if (
      constraints.some((constraint) => constraint.type === 'UNIQUE' && inColumns(constraint.columns)) ||
      indexes.some((index) => index.unique === true && inColumns(index.columns))
    )
      labels.push('UQ')
    for (const index of indexes) if (index.unique !== true && inColumns(index.columns)) labels.push(String(index.name ?? ''))
    return labels.filter(Boolean)
  }

  const setSelectedKeys = (keys: string[]) => {
    const allowedKeys = [...new Set(keys.filter((key) => fieldByKey.has(key)))]
    const existingByKey = new Map(
      selects.map((select) => {
        const field = asRecord(select.field)
        return [queryFieldKey(String(field.source ?? DATABASE_QUERY_SOURCE_ALIAS), String(field.column ?? '')), String(select.alias ?? '')]
      }),
    )
    const usedAliases = new Set<string>()
    const nextSelect = allowedKeys.map((key, index) => {
      const field = fieldByKey.get(key)!
      let alias = existingByKey.get(key) || queryOutputAlias(field.source, field.column)
      const baseAlias = alias
      let suffix = 2
      while (usedAliases.has(alias)) {
        const suffixText = `_${suffix++}`
        alias = `${baseAlias.slice(0, 64 - suffixText.length)}${suffixText}`
      }
      usedAliases.add(alias)
      return { field: { source: field.source, column: field.column }, alias: alias || `field_${index + 1}` }
    })
    const allowedAliases = new Set(nextSelect.map((item) => item.alias))
    updateAst({ select: nextSelect, orderBy: orders.filter((order) => allowedAliases.has(String(order.outputAlias ?? ''))) })
  }
  const toggleSelect = (key: string) =>
    setSelectedKeys(selectedKeys.includes(key) ? selectedKeys.filter((item) => item !== key) : [...selectedKeys, key])

  const addJoin = async () => {
    if (!resource || !joinTableId) return
    const target = tableResources.find((item) => item.id === joinTableId)
    if (!target) return
    setManualJoinError('')
    try {
      const [leftMetadata, rightMetadata] = await Promise.all([ensureTableMetadata(resource), ensureTableMetadata(target)])
      const leftColumn = String((Array.isArray(leftMetadata.columns) ? asRecord(leftMetadata.columns[0]).name : '') ?? '')
      const rightColumn = String((Array.isArray(rightMetadata.columns) ? asRecord(rightMetadata.columns[0]).name : '') ?? '')
      if (!leftColumn || !rightColumn) throw new Error('关联表缺少可用字段，无法创建 JOIN')
      const usedAliases = new Set(sourceEntries.map((entry) => entry.alias))
      let number = joins.length + 1
      while (usedAliases.has(`j${number}`)) number += 1
      const alias = `j${number}`
      updateAst({
        joins: [
          ...joins,
          {
            type: 'INNER',
            source: { alias, resourceId: target.external_id },
            on: [
              {
                left: { source: DATABASE_QUERY_SOURCE_ALIAS, column: leftColumn },
                operator: 'eq',
                right: { source: alias, column: rightColumn },
              },
            ],
          },
        ],
      })
      setJoinTableId('')
    } catch (err) {
      setManualJoinError(err instanceof Error ? err.message : '添加关联表失败')
    }
  }

  const updateJoin = (index: number, patch: Record<string, unknown>) =>
    updateAst({ joins: joins.map((join, joinIndex) => (joinIndex === index ? { ...join, ...patch } : join)) })
  const updateJoinOn = (index: number, side: 'left' | 'right', fieldKey: string) => {
    const join = joins[index]
    const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
    updateJoin(index, { on: [{ ...on, [side]: queryFieldRef(fieldKey), operator: 'eq' }] })
  }
  const removeJoin = (index: number) => {
    const removedAlias = String(asRecord(joins[index].source).alias ?? '')
    const fallbackField = fieldOptions.find((field) => field.source === DATABASE_QUERY_SOURCE_ALIAS)
    const remainingJoins = joins
      .filter((_, joinIndex) => joinIndex !== index)
      .map((join) => {
        const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
        const left = asRecord(on.left)
        return left.source === removedAlias && fallbackField
          ? { ...join, on: [{ ...on, left: { source: fallbackField.source, column: fallbackField.column } }] }
          : join
      })
    const remainingSelects = selects.filter((select) => String(asRecord(select.field).source ?? '') !== removedAlias)
    const remainingFilters = filters.filter((filter) => filter.source !== removedAlias)
    const allowedAliases = new Set(remainingSelects.map((select) => String(select.alias ?? '')))
    updateAst({
      joins: remainingJoins,
      select: remainingSelects,
      orderBy: orders.filter((order) => allowedAliases.has(String(order.outputAlias ?? ''))),
      ...encodeFilters(remainingFilters),
    })
  }

  const addFilter = () => {
    const field = fieldOptions[0]
    if (field) writeFilters([...filters, { source: field.source, column: field.column, operator: 'eq', value: '' }])
  }
  const updateFilter = (index: number, patch: Record<string, unknown>) =>
    writeFilters(filters.map((filter, filterIndex) => (filterIndex === index ? { ...filter, ...patch } : filter)))
  const removeFilter = (index: number) => writeFilters(filters.filter((_, filterIndex) => filterIndex !== index))
  const addOrder = () => updateAst({ orderBy: [...orders, { outputAlias: String(selectedAliases[0] ?? ''), direction: 'asc' }] })
  const updateOrder = (index: number, patch: Record<string, unknown>) =>
    updateAst({ orderBy: orders.map((order, orderIndex) => (orderIndex === index ? { ...order, ...patch } : order)) })
  const removeOrder = (index: number) => updateAst({ orderBy: orders.filter((_, orderIndex) => orderIndex !== index) })

  const joinsReady = joins.every((join) => {
    const source = asRecord(join.source)
    const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
    const left = asRecord(on.left)
    const right = asRecord(on.right)
    return Boolean(source.alias && source.resourceId && left.source && left.column && right.source && right.column)
  })

  const runPreview = useCallback(async () => {
    if (!resource) return
    setPreviewLoading(true)
    onPreviewLoadingChange(true)
    setPreviewError('')
    setPreview(null)
    try {
      const previewAst = normalizedDatabaseQueryAst(parameters.ast, resource)
      const started = await startExecution({
        taskId: `ui-db-preview-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`,
        resourceId: resource.id,
        action: 'database.query.preview',
        actionVersion: 1,
        parameters: { ast: previewAst },
        reason: '受治理多表查询预检',
      })
      const completed = await waitForTerminalExecution(started)
      if (completed.status !== 'SUCCEEDED') throw new Error(completed.errorMessage || completed.errorCode || '查询预检失败')
      const previewHash = String(completed.result?.previewHash ?? completed.result?.planHash ?? '')
      if (!/^[a-f0-9]{64}$/.test(previewHash)) throw new Error('预检未返回有效 previewHash')
      setPreview(completed.result ?? {})
      onPreviewVerdictChange(String(completed.result?.verdict ?? ''))
      setParameters((current) => ({ ...current, ast: previewAst, previewHash }))
    } catch (err) {
      onPreviewVerdictChange('')
      setPreviewError(err instanceof Error ? err.message : '查询预检失败')
    } finally {
      setPreviewLoading(false)
      onPreviewLoadingChange(false)
    }
  }, [onPreviewLoadingChange, onPreviewVerdictChange, parameters.ast, resource, setParameters, startExecution])

  useEffect(() => {
    if (previewRequest <= 0 || previewRequest === handledPreviewRequest.current) return
    handledPreviewRequest.current = previewRequest
    void runPreview()
  }, [previewRequest, runPreview])
  useEffect(
    () => () => {
      onPreviewLoadingChange(false)
      onPreviewVerdictChange('')
    },
    [onPreviewLoadingChange, onPreviewVerdictChange],
  )

  const usedResourceIDs = new Set([resource?.id ?? '', ...sourceEntries.slice(1).map((entry) => entry.table.id)])
  const availableJoinTables = tableResources.filter((table) => !usedResourceIDs.has(table.id))
  const rowStat = Number(baseMetadata?.estimatedRows ?? 0)
  return (
    <fieldset className="database-query-builder">
      <legend>返回字段与筛选（支持多表 JOIN）</legend>
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
          <button type="button" className="quiet-button" onClick={() => baseMetadataQuery.reload()}>
            重试
          </button>
        </div>
      ) : baseMetadata ? (
        <>
          <div className="database-builder-meta">
            主表 {resource.namespace ? `${resource.namespace}.` : ''}
            {resource.name} · {fieldOptions.filter((field) => field.source === DATABASE_QUERY_SOURCE_ALIAS).length} 列 ·{' '}
            {rowStat > 0 ? `约 ${rowStat.toLocaleString()} 行` : '行数未知'} · 当前 {sourceEntries.length} 张表
          </div>
          <div className="database-builder-filters">
            <div className="database-builder-filter-head">
              <strong>关联表</strong>
              <span>仅可关联同一数据库集成中的活动表，最多 8 个 JOIN</span>
            </div>
            <div className="database-builder-filter-row">
              <select
                value={joinTableId}
                onChange={(event) => setJoinTableId(event.target.value)}
                disabled={tableLoading || joins.length >= 8}
              >
                <option value="">选择要关联的表</option>
                {availableJoinTables.map((table) => (
                  <option key={table.id} value={table.id}>
                    {table.namespace ? `${table.namespace}.` : ''}
                    {table.name}
                  </option>
                ))}
              </select>
              <button
                type="button"
                className="quiet-button"
                disabled={!joinTableId || tableLoading || joins.length >= 8}
                onClick={() => void addJoin()}
              >
                {tableLoading ? '读取表目录…' : '添加 JOIN'}
              </button>
            </div>
            {joinError && (
              <div className="form-error" role="alert">
                {joinError}
              </div>
            )}
            {joins.length === 0 ? (
              <div className="onboarding-hint">尚未关联其它表；添加后可配置 INNER/LEFT JOIN 与两侧字段。</div>
            ) : (
              joins.map((join, index) => {
                const source = asRecord(join.source)
                const alias = String(source.alias ?? '')
                const table = sourceEntries.find((entry) => entry.alias === alias)?.table
                const on = asRecord((Array.isArray(join.on) ? join.on : [])[0])
                const left = asRecord(on.left)
                const right = asRecord(on.right)
                const leftFields = fieldOptions.filter((field) =>
                  sourceEntries.slice(0, index + 1).some((entry) => entry.alias === field.source),
                )
                const rightFields = fieldOptions.filter((field) => field.source === alias)
                return (
                  <div className="database-builder-filter-row" key={`${alias}:${String(source.resourceId)}`}>
                    <select value={String(join.type ?? 'INNER')} onChange={(event) => updateJoin(index, { type: event.target.value })}>
                      <option value="INNER">INNER JOIN</option>
                      <option value="LEFT">LEFT JOIN</option>
                    </select>
                    <span>{table ? `${table.namespace ? `${table.namespace}.` : ''}${table.name}` : String(source.resourceId)}</span>
                    <select
                      value={queryFieldKey(String(left.source ?? ''), String(left.column ?? ''))}
                      onChange={(event) => updateJoinOn(index, 'left', event.target.value)}
                    >
                      {leftFields.map((field) => (
                        <option key={field.key} value={field.key}>
                          {field.table.name}.{field.column}
                        </option>
                      ))}
                    </select>
                    <span>=</span>
                    <select
                      value={queryFieldKey(String(right.source ?? ''), String(right.column ?? ''))}
                      onChange={(event) => updateJoinOn(index, 'right', event.target.value)}
                    >
                      {rightFields.map((field) => (
                        <option key={field.key} value={field.key}>
                          {field.table.name}.{field.column}
                        </option>
                      ))}
                    </select>
                    <button type="button" className="icon-button" onClick={() => removeJoin(index)} aria-label="删除关联表">
                      <X size={14} />
                    </button>
                  </div>
                )
              })
            )}
          </div>
          <div className="database-builder-selection-head">
            <div>
              <strong>返回字段</strong>
              <small>
                已选择 {selectedKeys.length} / {fieldOptions.length} 个字段，可跨表多选
              </small>
            </div>
            <div>
              <button
                type="button"
                className="quiet-button"
                disabled={selectedKeys.length === fieldOptions.length}
                onClick={() => setSelectedKeys(fieldOptions.map((field) => field.key))}
              >
                全选
              </button>
              <button type="button" className="quiet-button" disabled={selectedKeys.length === 0} onClick={() => setSelectedKeys([])}>
                清空
              </button>
            </div>
          </div>
          <div className="database-builder-columns" aria-label="选择返回字段（支持跨表多选）">
            {fieldOptions.map((field) => (
              <label key={field.key} className={selectedKeys.includes(field.key) ? 'selected' : ''}>
                <input type="checkbox" checked={selectedKeys.includes(field.key)} onChange={() => toggleSelect(field.key)} />
                <span className="database-column-details">
                  <strong>
                    {field.table.name}.{field.column}
                  </strong>
                  <small>
                    {String(field.definition.dataType)}
                    {field.definition.nullable === true ? ' · 可空' : ''} · 别名 {field.source}
                  </small>
                </span>
                {indexLabels(field.metadata, field.column).map((label) => (
                  <code className="index-badge" key={label}>
                    {label}
                  </code>
                ))}
              </label>
            ))}
          </div>
          <div className="database-builder-filters">
            <div className="database-builder-filter-head">
              <strong>筛选条件</strong>
              <button type="button" className="quiet-button" disabled={fieldOptions.length === 0} onClick={addFilter}>
                添加筛选
              </button>
            </div>
            {filters.length === 0 ? (
              <div className="onboarding-hint">未添加筛选，将按 limit 返回查询结果。</div>
            ) : (
              filters.map((filter, index) => {
                const key = queryFieldKey(filter.source, filter.column)
                const field = fieldByKey.get(key)
                return (
                  <div className="database-builder-filter-row" key={index}>
                    <select value={key} onChange={(event) => updateFilter(index, queryFieldRef(event.target.value))}>
                      {fieldOptions.map((option) => (
                        <option key={option.key} value={option.key}>
                          {option.table.name}.{option.column}
                        </option>
                      ))}
                    </select>
                    <select value={filter.operator} onChange={(event) => updateFilter(index, { operator: event.target.value })}>
                      {DATABASE_FILTER_OPERATORS.map((value) => (
                        <option key={value} value={value}>
                          {value}
                        </option>
                      ))}
                    </select>
                    <DatabaseFilterValueInput
                      column={field?.definition}
                      operator={filter.operator}
                      value={filter.value}
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
              <button type="button" className="quiet-button" disabled={selectedAliases.length === 0} onClick={addOrder}>
                添加排序
              </button>
            </div>
            {orders.length === 0 ? (
              <div className="onboarding-hint">未添加排序。</div>
            ) : (
              orders.map((order, index) => (
                <div className="database-builder-filter-row" key={index}>
                  <select
                    value={String(order.outputAlias ?? '')}
                    onChange={(event) => updateOrder(index, { outputAlias: event.target.value })}
                  >
                    {selects.map((select) => {
                      const field = asRecord(select.field)
                      return (
                        <option key={String(select.alias)} value={String(select.alias)}>
                          {String(field.source)}.{String(field.column)} → {String(select.alias)}
                        </option>
                      )
                    })}
                  </select>
                  <select
                    value={String(order.direction ?? 'asc')}
                    onChange={(event) => updateOrder(index, { direction: event.target.value })}
                  >
                    <option value="asc">asc</option>
                    <option value="desc">desc</option>
                  </select>
                  <button type="button" className="icon-button" onClick={() => removeOrder(index)} aria-label="删除排序">
                    <X size={14} />
                  </button>
                </div>
              ))
            )}
          </div>
          <label>
            <span>返回行数（limit）</span>
            <input
              type="number"
              min={1}
              max={1000}
              value={limit}
              onChange={(event) =>
                updateAst({ limit: event.target.value === '' ? undefined : Math.max(1, Math.min(1000, Number(event.target.value))) })
              }
            />
          </label>
          <div className="database-builder-filters">
            <div className="database-builder-filter-head">
              <strong>预检</strong>
              <button
                type="button"
                className="secondary-button preview-button"
                disabled={previewLoading || selectedKeys.length === 0 || !joinsReady}
                onClick={runPreview}
              >
                {previewLoading ? <Loader2 size={16} className="spin" /> : <ShieldCheck size={16} />}
                {previewLoading ? '正在预检' : '预览并生成 previewHash'}
              </button>
            </div>
            {previewError && (
              <div className="form-error" role="alert">
                {previewError}
              </div>
            )}
            {preview && (
              <div className="database-builder-meta">
                <strong>SQL：</strong>
                <code>{String(preview.sql ?? '-')}</code> · 判定 {String(preview.verdict ?? '-')} · 预估扫描{' '}
                {String(preview.estimatedRows ?? '-')} 行
                {Array.isArray(preview.warnings) && preview.warnings.length > 0 ? ` · 告警 ${preview.warnings.length} 条` : ''}
              </div>
            )}
          </div>
        </>
      ) : null}
    </fieldset>
  )
}
