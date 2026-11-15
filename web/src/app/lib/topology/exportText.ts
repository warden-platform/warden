// Generate an AI-readable, structured plain-text export of the organization
// topology. Designed so an external AI Agent can paste this into context and
// understand what infrastructure exists, how it connects, and which actions
// it may request access to.
import type { ApiAction, ApiConnector, ApiExecution, ApiResource, Integration } from '../../../shared/api'
import { connectorTypeLabel, mapConnectorStatus } from '../../../shared/types'

function heartbeatAgo(c: ApiConnector): string {
  if (!c.last_heartbeat) return 'no heartbeat'
  const secs = Math.max(0, Math.round((Date.now() - new Date(c.last_heartbeat).getTime()) / 1000))
  return secs < 60 ? `${secs}s ago` : `${Math.round(secs / 60)}m ago`
}

export function exportTopologyText(
  connectors: ApiConnector[],
  integrations: Integration[],
  resources: ApiResource[],
  actions: ApiAction[],
  executions: ApiExecution[],
): string {
  const lines: string[] = []
  lines.push('# Warden Organization Topology')
  lines.push('')
  lines.push(`Generated: ${new Date().toISOString()}`)
  lines.push(
    `Connectors: ${connectors.length} | Integrations: ${integrations.length} | Resources: ${resources.length} | Actions: ${actions.length} | Recent executions (24h): ${executions.filter((e) => Date.now() - new Date(e.createdAt).getTime() < 86400000).length}`,
  )
  lines.push('')

  lines.push('## Connectors')
  for (const c of connectors) {
    const cIntegrations = integrations.filter((i) => i.connector_id === c.id)
    const cResources = resources.filter((r) => r.connector_id === c.id)
    const namespaces = (c.available_namespaces ?? []).slice(0, 12)
    lines.push(`- ${c.name} [${connectorTypeLabel(c.connector_type)}, ${mapConnectorStatus(c.status)}, v${c.version}]`)
    lines.push(`  - ID: ${c.id}`)
    lines.push(`  - Environment: ${c.environment}`)
    lines.push(`  - Heartbeat: ${heartbeatAgo(c)}`)
    if (c.available_namespaces?.length)
      lines.push(
        `  - Namespaces: ${namespaces.join(', ')}${c.available_namespaces.length > 12 ? `, +${c.available_namespaces.length - 12} more` : ''}`,
      )
    if (c.available_services?.length) lines.push(`  - Services: ${c.available_services.slice(0, 12).join(', ')}`)
    lines.push(`  - Integrations: ${cIntegrations.length ? cIntegrations.map((i) => i.name).join(', ') : 'none'}`)
    if (cResources.length) {
      const byType = new Map<string, number>()
      for (const r of cResources) byType.set(r.resource_type, (byType.get(r.resource_type) ?? 0) + 1)
      lines.push(`  - Resources: ${[...byType.entries()].map(([t, n]) => `${n} ${t}`).join(', ')}`)
    }
    lines.push('')
  }

  lines.push('## Integrations')
  for (const i of integrations) {
    const conn = connectors.find((c) => c.id === i.connector_id)
    const iResources = resources.filter((r) => r.integration_id === i.id)
    const scope = i.scope as { allowedJobs?: string[] }
    lines.push(`- ${i.name} [${i.protocol}, ${i.status}]`)
    lines.push(`  - Endpoint: ${i.endpoint}`)
    lines.push(`  - Connector: ${conn?.name ?? i.connector_id}`)
    if (i.username) lines.push(`  - Username: ${i.username}`)
    if (iResources.length) {
      const byType = new Map<string, number>()
      for (const r of iResources) byType.set(r.resource_type, (byType.get(r.resource_type) ?? 0) + 1)
      lines.push(`  - Resources: ${[...byType.entries()].map(([t, n]) => `${n} ${t}`).join(', ')}`)
    }
    if (scope?.allowedJobs?.length) {
      lines.push(
        `  - Allowed Jobs: ${scope.allowedJobs.slice(0, 20).join(', ')}${scope.allowedJobs.length > 20 ? `, +${scope.allowedJobs.length - 20} more` : ''}`,
      )
    } else {
      lines.push(`  - Allowed Jobs: all`)
    }
    if (i.last_error) lines.push(`  - Last Error: ${i.last_error}`)
    lines.push('')
  }

  lines.push('## Available Actions')
  const byResourceType = new Map<string, string[]>()
  for (const a of actions) {
    const list = byResourceType.get(a.resource_type) ?? []
    list.push(a.name)
    byResourceType.set(a.resource_type, list)
  }
  if (byResourceType.size === 0) {
    lines.push('(no actions discovered)')
  } else {
    for (const [type, list] of byResourceType) {
      lines.push(`- ${type}: ${list.join(', ')}`)
    }
  }
  lines.push('')

  lines.push('## Recent Executions (24h)')
  const recent = executions.filter((e) => Date.now() - new Date(e.createdAt).getTime() < 86400000).slice(0, 20)
  if (recent.length === 0) {
    lines.push('(none in the last 24h)')
  } else {
    for (const e of recent) {
      const res = resources.find((r) => r.id === e.resourceId)
      lines.push(`- ${e.createdAt} ${e.status} ${e.actionId} on ${res?.name ?? e.resourceId.slice(0, 8)} (trace ${e.traceId.slice(0, 8)})`)
    }
  }

  return lines.join('\n')
}
