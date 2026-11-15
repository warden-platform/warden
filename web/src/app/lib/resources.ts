// Resource domain derivation + Jenkins build grouping helpers.
// Resources arrive as a flat list from the API; the resources page splits
// them into three domains (Kubernetes / Linux / Jenkins) so each can be
// presented with a layout that matches its data shape.
import type { ApiResource } from '../../shared/api'

export type ResourceDomain = 'kubernetes' | 'linux' | 'jenkins' | 'grafana' | 'database' | 'huawei' | 'network'

const K8S_TYPES = new Set([
  'pod',
  'deployment',
  'statefulset',
  'daemonset',
  'replicaset',
  'job',
  'cronjob',
  'service',
  'endpoints',
  'ingress',
  'configmap',
  'secret',
  'event',
  'node',
  'namespace',
])

export function resourceTypeDomain(t: string): ResourceDomain {
  if (K8S_TYPES.has(t)) return 'kubernetes'
  if (t.startsWith('jenkins')) return 'jenkins'
  if (t.startsWith('grafana')) return 'grafana'
  if (t.startsWith('database')) return 'database'
  if (t.startsWith('huawei') || t.startsWith('aliyun_sls')) return 'huawei'
  if (t.startsWith('network_')) return 'network'
  return 'linux' // linux_host, systemd_service, docker_container
}

export function resourceDomain(r: ApiResource): ResourceDomain {
  return resourceTypeDomain(r.resource_type)
}

export function domainLabel(d: ResourceDomain): string {
  return d === 'kubernetes'
    ? 'Kubernetes'
    : d === 'linux'
      ? 'Linux 主机'
      : d === 'jenkins'
        ? 'Jenkins'
        : d === 'grafana'
          ? 'Grafana'
          : d === 'huawei'
            ? '云厂商'
            : d === 'network'
              ? '网络设备'
              : '数据库'
}

// Jenkins build metadata shape: { job, number, timestamp }.
type BuildMeta = { job?: string; number?: number; timestamp?: number }

export function buildJobName(r: ApiResource): string | undefined {
  return (r.metadata as BuildMeta).job
}
export function buildNumber(r: ApiResource): number | undefined {
  return (r.metadata as BuildMeta).number
}
export function buildTimestamp(r: ApiResource): number | undefined {
  return (r.metadata as BuildMeta).timestamp
}

// Jenkins job metadata shape: { buildable, inQueue, ... }.
type JobMeta = { buildable?: boolean; inQueue?: boolean; lastBuild?: number }

export function jobBuildable(r: ApiResource): boolean {
  return (r.metadata as JobMeta).buildable !== false
}

// Group builds by their parent job name (metadata.job), each list sorted by
// build number descending so the most recent builds come first.
export function groupBuildsByJob(builds: ApiResource[]): Map<string, ApiResource[]> {
  const m = new Map<string, ApiResource[]>()
  for (const b of builds) {
    const job = buildJobName(b)
    if (!job) continue
    ;(m.get(job) ?? m.set(job, []).get(job)!).push(b)
  }
  for (const [, arr] of m) arr.sort((a, b) => (buildNumber(b) ?? 0) - (buildNumber(a) ?? 0))
  return m
}

// Format a Jenkins build timestamp (ms epoch) as a compact date-time.
export function formatBuildTime(ms: number | undefined): string {
  if (!ms) return '-'
  return new Date(ms).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}
