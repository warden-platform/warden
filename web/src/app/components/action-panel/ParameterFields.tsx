import type { Dispatch, SetStateAction } from 'react'
import { ChevronDown } from 'lucide-react'
import type { ApiProject } from '../../../shared/api'
import { cloudRegionLabel, parameterLabel, parameterPlaceholder } from '../../lib/labels'
import type { ParameterSchema } from './constants'
import { asRecord, epochToLocalInput, isCloudLogTimeParameter } from './utils'

// 动态参数表单：按 action 的 parameter_schema 生成输入控件，
// 内含 Jenkins SCM、云日志时间、容器选择等特化渲染。

export function LogTimePresets({
  actionName,
  setParameters,
}: {
  actionName: string
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
}) {
  const applyRange = (minutes: number) => {
    const now = Date.now()
    setParameters((current) =>
      actionName === 'huawei.lts.logs.query'
        ? { ...current, startTime: now - minutes * 60 * 1000, endTime: now }
        : { ...current, from: Math.floor(now / 1000) - minutes * 60, to: Math.floor(now / 1000) },
    )
  }
  return (
    <fieldset className="log-time-presets">
      <legend>时间范围</legend>
      {[
        [15, '最近 15 分钟'],
        [60, '最近 1 小时'],
        [360, '最近 6 小时'],
        [1440, '最近 24 小时'],
      ].map(([minutes, label]) => (
        <button key={minutes} type="button" onClick={() => applyRange(Number(minutes))}>
          {label}
        </button>
      ))}
      <small>也可以在下方选择任意历史起止时间，实际可查范围由云日志保留策略决定。</small>
    </fieldset>
  )
}

export function ActionParameterFields({
  entries,
  parameters,
  setParameters,
  requiredParameters,
  resourceContainers,
  selectedContainer,
  actionName,
  projects,
}: {
  entries: [string, ParameterSchema][]
  parameters: Record<string, unknown>
  setParameters: Dispatch<SetStateAction<Record<string, unknown>>>
  requiredParameters: string[]
  resourceContainers: { name: string; image: string }[]
  selectedContainer?: { name: string; image: string }
  actionName: string
  projects: ApiProject[]
}) {
  return (
    <>
      {entries.map(([key, schema]) => {
        if (
          (actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update') &&
          key === 'pipelineScript' &&
          parameters.mode !== 'inline'
        )
          return null
        if ((actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update') && key === 'scm' && parameters.mode !== 'scm')
          return null
        if (key === 'container' && resourceContainers.length > 0) {
          return (
            <fieldset className="action-picker container-picker" key={key}>
              <legend>选择容器</legend>
              {resourceContainers.map((container) => (
                <label key={container.name} className={parameters.container === container.name ? 'selected' : ''}>
                  <input
                    type="radio"
                    name="container"
                    checked={parameters.container === container.name}
                    onChange={() => setParameters((current) => ({ ...current, container: container.name }))}
                  />
                  <span>
                    <strong>{container.name}</strong>
                    <small>{container.image}</small>
                  </span>
                </label>
              ))}
            </fieldset>
          )
        }
        if (key === 'projectId' && actionName === 'huawei.ecs.create') {
          return (
            <label key={key}>
              <span>{parameterLabel(key, actionName)}</span>
              <div className="select-wrap full">
                <select
                  value={String(parameters[key] ?? '')}
                  required
                  onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.value }))}
                >
                  <option value="">选择 PAM 项目</option>
                  {projects
                    .filter((project) => !project.is_default)
                    .map((project) => (
                      <option key={project.id} value={project.id}>
                        {project.name}
                      </option>
                    ))}
                </select>
                <ChevronDown size={15} />
              </div>
            </label>
          )
        }
        if (isCloudLogTimeParameter(actionName, key)) {
          const unit = actionName === 'huawei.lts.logs.query' ? 1 : 1000
          return (
            <label key={key}>
              <span>{parameterLabel(key, actionName)}</span>
              <input
                type="datetime-local"
                step="1"
                value={epochToLocalInput(Number(parameters[key] ?? 0) * unit)}
                required={requiredParameters.includes(key)}
                onChange={(event) => {
                  const epoch = new Date(event.target.value).getTime()
                  if (Number.isFinite(epoch)) setParameters((current) => ({ ...current, [key]: Math.floor(epoch / unit) }))
                }}
              />
            </label>
          )
        }
        if (schema.enum?.length) {
          return (
            <label key={key}>
              <span>{parameterLabel(key, actionName)}</span>
              <div className="select-wrap full">
                <select
                  value={String(parameters[key] ?? '')}
                  required={requiredParameters.includes(key)}
                  onChange={(event) =>
                    setParameters((current) => {
                      const next = { ...current, [key]: event.target.value }
                      if (key === 'mode' && (actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update')) {
                        delete next.pipelineScript
                        delete next.scm
                      }
                      return next
                    })
                  }
                >
                  <option value="">请选择</option>
                  {schema.enum.map((value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  ))}
                </select>
                <ChevronDown size={15} />
              </div>
            </label>
          )
        }
        if (schema.type === 'boolean') {
          return (
            <label className="check-field" key={key}>
              <input
                type="checkbox"
                checked={Boolean(parameters[key])}
                onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.checked }))}
              />
              <span>{parameterLabel(key, actionName)}</span>
            </label>
          )
        }
        if (schema.type === 'object' || schema.type === 'array') {
          if (key === 'scm' && (actionName === 'jenkins.job.create' || actionName === 'jenkins.job.update')) {
            const scm = asRecord(parameters.scm)
            const setScmField = (field: string, value: string) =>
              setParameters((current) => ({ ...current, scm: { ...asRecord(current.scm), [field]: value } }))
            return (
              <fieldset className="jenkins-scm-fields" key={key}>
                <legend>SCM Pipeline</legend>
                <label>
                  <span>仓库地址</span>
                  <input
                    type="text"
                    value={String(scm.repoUrl ?? '')}
                    required
                    onChange={(event) => setScmField('repoUrl', event.target.value)}
                    placeholder="ssh://git@example.com/team/app.git"
                  />
                </label>
                <label>
                  <span>Credential ID</span>
                  <input
                    type="text"
                    value={String(scm.credentialsId ?? '')}
                    onChange={(event) => setScmField('credentialsId', event.target.value)}
                    placeholder="deploy-key"
                  />
                </label>
                <label>
                  <span>分支</span>
                  <input
                    type="text"
                    value={String(scm.branch ?? '')}
                    onChange={(event) => setScmField('branch', event.target.value)}
                    placeholder="*/main"
                  />
                </label>
                <label>
                  <span>Jenkinsfile 路径</span>
                  <input
                    type="text"
                    value={String(scm.scriptPath ?? '')}
                    onChange={(event) => setScmField('scriptPath', event.target.value)}
                    placeholder="Jenkinsfile"
                  />
                </label>
                <div className="manifest-security-note">
                  仅填写 Jenkins Credential ID；不得输入 API Token、密码、私钥或 Authorization 值。
                </div>
              </fieldset>
            )
          }
          return (
            <label key={key}>
              <span>{parameterLabel(key, actionName)}</span>
              <textarea
                rows={4}
                value={typeof parameters[key] === 'object' ? JSON.stringify(parameters[key], null, 2) : String(parameters[key] ?? '')}
                onChange={(event) =>
                  setParameters((current) => {
                    const text = event.target.value.trim()
                    if (!text) {
                      const next = { ...current }
                      delete next[key]
                      return next
                    }
                    try {
                      return { ...current, [key]: JSON.parse(text) }
                    } catch {
                      return { ...current, [key]: event.target.value }
                    }
                  })
                }
                required={requiredParameters.includes(key) || (key === 'scm' && parameters.mode === 'scm')}
                placeholder={schema.type === 'array' ? '["id-1"]' : parameterPlaceholder(key)}
              />
            </label>
          )
        }
        if (key === 'pipelineScript') {
          return (
            <label key={key}>
              <span>{parameterLabel(key, actionName)}</span>
              <textarea
                rows={8}
                value={String(parameters[key] ?? '')}
                onChange={(event) => setParameters((current) => ({ ...current, [key]: event.target.value }))}
                required={requiredParameters.includes(key) || parameters.mode === 'inline'}
                placeholder={parameterPlaceholder(key)}
              />
            </label>
          )
        }
        const readOnlyRegion = key === 'region' && actionName === 'huawei.ecs.create'
        return (
          <label key={key}>
            <span>{parameterLabel(key, actionName)}</span>
            {key === 'image' && selectedContainer && (
              <small className="current-image">
                当前镜像：<code>{selectedContainer.image}</code>
              </small>
            )}
            <input
              type={schema.type === 'integer' || schema.type === 'number' ? 'number' : 'text'}
              min={schema.minimum}
              max={schema.maximum}
              readOnly={readOnlyRegion}
              value={readOnlyRegion ? cloudRegionLabel(String(parameters[key] ?? '')) : String(parameters[key] ?? '')}
              onChange={(event) =>
                setParameters((current) => ({
                  ...current,
                  [key]: schema.type === 'integer' || schema.type === 'number' ? Number(event.target.value) : event.target.value,
                }))
              }
              required={requiredParameters.includes(key)}
              placeholder={parameterPlaceholder(key)}
            />
          </label>
        )
      })}
    </>
  )
}
