// Generate an AI-readable, structured plain-text export of the organization
// topology from the drill-down tree response.
import type { TopologyGroup, TopologyTree } from '../../../shared/api'
import { connectorTypeLabel, mapConnectorStatus } from '../../../shared/types'

function groupLines(prefix: string, group: TopologyGroup): string[] {
  const actions = group.actions.length ? ` | Actions: ${group.actions.map((a) => a.name).join(', ')}` : ''
  const samples = group.samples.length ? ` | Samples: ${group.samples.map((s) => (s.namespace ? `${s.namespace}/${s.name}` : s.name)).join(', ')}` : ''
  return [`${prefix}- ${group.type}: ${group.count} (${group.health}) live=${group.live ? 'yes' : 'no'}${actions}${samples}`]
}

export function exportTopologyText(tree: TopologyTree): string {
  const lines: string[] = []
  lines.push('# Warden Organization Topology')
  lines.push('')
  lines.push(`Generated: ${new Date().toISOString()}`)
  lines.push(
    `Connectors: ${tree.summary.connectors} | Integrations: ${tree.summary.integrations} | Groups: ${tree.summary.groups} | Resources: ${tree.summary.resources} | Actions: ${tree.summary.actions}`,
  )
  lines.push('')

  lines.push('## Connectors')
  for (const connector of tree.connectors) {
    lines.push(`- ${connector.name} [${connectorTypeLabel(connector.type)}, ${mapConnectorStatus(connector.status)}, v${connector.version}]`)
    lines.push(`  - ID: ${connector.id}`)
    lines.push(`  - Live: ${connector.live ? 'yes' : 'no'}`)
    for (const group of connector.groups) {
      lines.push(...groupLines('  ', group))
    }
    for (const integration of connector.integrations) {
      lines.push(`  - Integration ${integration.name} [${integration.protocol}, ${integration.status}]`)
      lines.push(`    - Endpoint: ${integration.endpoint}`)
      lines.push(`    - Live: ${integration.live ? 'yes' : 'no'}`)
      for (const group of integration.groups) {
        lines.push(...groupLines('    ', group))
      }
      lines.push('')
    }
    lines.push('')
  }

  lines.push('## Available Actions')
  const byResourceType = new Map<string, string[]>()
  for (const connector of tree.connectors) {
    const groups = [...connector.groups, ...connector.integrations.flatMap((integration) => integration.groups)]
    for (const group of groups) {
      const list = byResourceType.get(group.type) ?? []
      for (const action of group.actions) {
        if (!list.includes(action.name)) list.push(action.name)
      }
      if (list.length) byResourceType.set(group.type, list)
    }
  }
  if (byResourceType.size === 0) {
    lines.push('(no actions discovered)')
  } else {
    for (const [type, list] of byResourceType) {
      lines.push(`- ${type}: ${list.join(', ')}`)
    }
  }

  return lines.join('\n')
}
