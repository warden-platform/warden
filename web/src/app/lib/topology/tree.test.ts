import { describe, expect, it } from 'vitest'
import type { TopologyTree } from '../../../shared/api'
import { countLiveGroups, resourceDomains, resourceTotals, resourceTypeBreakdown } from './tree'

const tree: TopologyTree = {
  summary: { connectors: 1, integrations: 1, groups: 2, resources: 12, actions: 1 },
  connectors: [
    {
      id: 'c1',
      name: 'prod-cluster',
      type: 'kubernetes',
      version: '1.0.0',
      status: 'ACTIVE',
      live: false,
      integrations: [
        {
          id: 'i1',
          name: 'jenkins-ci',
          protocol: 'jenkins',
          endpoint: 'https://jenkins.example.test',
          status: 'ACTIVE',
          live: false,
          groups: [
            {
              id: 'group:i1:jenkins_build',
              type: 'jenkins_build',
              count: 10,
              activeCount: 8,
              status: 'warning',
              health: '8/10 可用',
              live: false,
              samples: [{ name: 'platform-build', status: 'ACTIVE' }],
              actions: [{ name: 'jenkins.job.get', displayName: '查看 Job', risk: 'low' }],
            },
          ],
        },
      ],
      groups: [
        {
          id: 'group:c1:deployment',
          type: 'deployment',
          count: 2,
          activeCount: 2,
          status: 'active',
          health: '全部正常',
          live: false,
          samples: [],
          actions: [],
        },
      ],
    },
  ],
}

describe('resourceTotals', () => {
  it('sums hosted resources and active counts across groups', () => {
    expect(resourceTotals(tree)).toEqual({ total: 12, active: 10, types: 2 })
  })
})

describe('resourceTypeBreakdown', () => {
  it('folds groups of the same type and orders by volume', () => {
    const breakdown = resourceTypeBreakdown(tree)
    expect(breakdown).toHaveLength(2)
    expect(breakdown[0]).toMatchObject({ type: 'jenkins_build', label: 'Jenkins Build', count: 10, activeCount: 8 })
    expect(breakdown[0].status).toBe('warning')
    expect(breakdown[0].health).toBe('8/10 可用')
    expect(breakdown[0].hosts).toBe(1)

    const deployment = breakdown[1]
    expect(deployment).toMatchObject({ type: 'deployment', label: 'Deployment', count: 2, activeCount: 2, status: 'active' })
  })

  it('aggregates the same type hosted across several locations', () => {
    const multi: TopologyTree = {
      ...tree,
      connectors: [
        {
          ...tree.connectors[0],
          groups: [
            { ...tree.connectors[0].groups[0], count: 4, activeCount: 3 },
            { id: 'group:c1:deployment-2', type: 'deployment', count: 5, activeCount: 5, status: 'active', health: '全部正常', live: false, samples: [], actions: [] },
          ],
        },
      ],
    }
    const deployment = resourceTypeBreakdown(multi).find((t) => t.type === 'deployment')
    expect(deployment).toMatchObject({ count: 9, activeCount: 8, hosts: 2 })
  })
})

describe('resourceDomains', () => {
  it('groups types under their ecosystem domain', () => {
    const domains = resourceDomains(tree)
    expect(domains.map((d) => d.domain).sort()).toEqual(['jenkins', 'kubernetes'])
    const jenkins = domains.find((d) => d.domain === 'jenkins')
    expect(jenkins).toMatchObject({ label: 'Jenkins', total: 10, active: 8, status: 'warning' })
    expect(jenkins?.types).toHaveLength(1)
  })
})

describe('countLiveGroups', () => {
  it('counts live groups across integrations and direct groups', () => {
    const liveTree: TopologyTree = {
      ...tree,
      connectors: [
        {
          ...tree.connectors[0],
          groups: [{ ...tree.connectors[0].groups[0], live: true }],
          integrations: [
            {
              ...tree.connectors[0].integrations[0],
              groups: [{ ...tree.connectors[0].integrations[0].groups[0], live: true }],
            },
          ],
        },
      ],
    }
    expect(countLiveGroups(liveTree)).toBe(2)
  })
})