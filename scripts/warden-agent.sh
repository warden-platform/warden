#!/bin/bash
# Warden Agent 客户端 - 封装 Warden API 调用，供外部 Agent 使用
#
# 用法:
#   warden-agent.sh <action> [args...]
#
# 子命令:
#   create-execution <action> <resource_id> <params_json> [reason]
#   create-readonly-execution <action> <resource_id> <params_json> [reason]
#   create-aiops-execution <action> <resource_id> <params_json> [reason]
#   get-execution <execution_id>
#   my-requests [status]
#   list-resources [type]
#   resource-summary [project] [environment] [type]
#   find-resources <query> [project] [environment] [type]
#   list-actions [resource_id]
#   diagnose-context <json>
#   diagnose-result <traceId>
#   wait-result <execution_id> [timeout_seconds]
#   wait-readonly-result <execution_id> [timeout_seconds]
#
# 环境变量:
#   WARDEN_API_ENDPOINT     Warden API 地址，例如 http://localhost:8080/api/v1 (必需)
#   WARDEN_AGENT_API_KEY    API Key，与 warden-control 的 WARDEN_AGENT_API_KEY 一致
#   WARDEN_ACTION_VERSION   显式覆盖 Action 版本；默认按动作选择当前版本
#   WARDEN_TRACE_ID         可选会话 trace id（非空时并入 create-execution 请求）
#
# 依赖: curl, jq

set -euo pipefail

if [[ -z "${WARDEN_API_ENDPOINT:-}" ]]; then
    echo "错误: 请设置 WARDEN_API_ENDPOINT 环境变量" >&2
    exit 1
fi

AUTH_HEADER=()
if [[ -n "${WARDEN_AGENT_API_KEY:-}" ]]; then
    AUTH_HEADER=(-H "Authorization: Bearer ${WARDEN_AGENT_API_KEY}")
fi

curl_json() {
    local method="$1"
    local path="$2"
    shift 2
    if [[ ${#AUTH_HEADER[@]} -gt 0 ]]; then
        curl --fail-with-body -sS -X "${method}" "${AUTH_HEADER[@]}" \
            -H "Content-Type: application/json" \
            "$@" "${WARDEN_API_ENDPOINT}${path}"
    else
        curl --fail-with-body -sS -X "${method}" \
            -H "Content-Type: application/json" \
            "$@" "${WARDEN_API_ENDPOINT}${path}"
    fi
}

is_readonly_action() {
    case "$1" in
        deployment.get|deployment.rollout_status|pod.logs.read|pod.get|pod.events|event.list|\
        statefulset.get|daemonset.get|replicaset.get|job.get|cronjob.get|service.get|endpoints.get|\
        ingress.get|configmap.get|node.get|namespace.list|\
        linux.host.info|linux.process.list|linux.disk.usage|systemd.service.status|systemd.service.logs|\
        docker.container.list|docker.container.inspect|docker.container.logs|\
        jenkins.controller.get|jenkins.job.get|jenkins.job.config.get|jenkins.build.get|jenkins.build.logs|\
        jenkins.queue.get|jenkins.node.get|jenkins.credential_reference.get|\
        grafana.instance.get|grafana.folder.list|grafana.folder.get|grafana.dashboard.list|\
        grafana.dashboard.get|grafana.dashboard.export|grafana.datasource.list|grafana.datasource.query|\
        database.instance.get|database.schema.list|database.schema.get|database.table.list|database.table.describe|database.table.rows.query|\
        huawei.account.get|huawei.ecs.get|huawei.ecs.metrics|huawei.rds.get|huawei.elb.get|huawei.obs.bucket.get|huawei.obs.object.list|huawei.obs.object.metadata.get|huawei.lts.logs.query|\
		pod.metrics|node.metrics|aliyun.sls.account.get|aliyun.sls.logs.query|provider.request)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

is_aiops_action() {
    is_readonly_action "$1" && return 0
	case "$1" in
		deployment.update_image|deployment.restart|deployment.scale|\
		statefulset.restart|statefulset.scale|statefulset.update_image|\
		cronjob.suspend|cronjob.resume|cronjob.trigger|\
		systemd.service.start|systemd.service.stop|systemd.service.restart|\
		docker.container.start|docker.container.stop|docker.container.restart|\
		huawei.ecs.create|huawei.ecs.start|huawei.ecs.stop|huawei.ecs.reboot|huawei.ecs.update_metadata|huawei.ecs.delete|huawei.obs.object.delete|\
		grafana.folder.create|grafana.folder.delete|grafana.dashboard.create|grafana.dashboard.update|grafana.dashboard.delete|\
        grafana.datasource.create|grafana.datasource.update|grafana.datasource.delete|\
        namespace.dashboard_configmap.apply|configmap.dashboard.delete|\
        jenkins.*|credential.*|kubernetes.resource.create|kubernetes.resource.update|kubernetes.resource.delete)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

create_execution() {
    local mode="$1"
    shift
    if [[ $# -lt 4 ]]; then
        echo "用法: warden-agent.sh create-execution <action> <resource_id> <params_json> [reason]" >&2
        exit 1
    fi
    local action="$2"
    if [[ "$mode" == "readonly" ]] && ! is_readonly_action "$action"; then
        echo "拒绝: Warden ReadOnly 不允许动作 ${action}" >&2
        exit 3
    fi
    if [[ "$mode" == "aiops" ]] && ! is_aiops_action "$action"; then
        echo "拒绝: Warden AIOps 不允许动作 ${action}" >&2
        exit 3
    fi
    local default_action_version=1
    if [[ "$action" == "deployment.update_image" ]]; then
        default_action_version=2
    fi
    local action_version="${WARDEN_ACTION_VERSION:-$default_action_version}"
    local payload
    payload=$(jq -n \
        --arg act "$action" \
        --arg rid "$3" \
        --argjson params "$4" \
        --arg reason "${5:-}" \
        --argjson av "$action_version" \
        --arg traceId "${WARDEN_TRACE_ID:-}" \
        '{action: $act, resourceId: $rid, parameters: $params, reason: $reason, actionVersion: $av} + (if $traceId != "" then {traceId: $traceId} else {} end)')
    # A fresh invocation must observe fresh infrastructure state. Callers that
    # retry the same HTTP request may explicitly reuse WARDEN_IDEMPOTENCY_KEY.
    local nonce="${WARDEN_IDEMPOTENCY_KEY:-$(date +%s)-$$-${RANDOM:-0}}"
    local idempotency_key
    idempotency_key="warden-agent-$(printf '%s' "${nonce}:${action}:${3}:${4}:${5:-}" | shasum -a 256 | awk '{print $1}')"
    curl_json POST /executions -H "Idempotency-Key: ${idempotency_key}" -d "$payload"
    echo
}

wait_execution() {
    local readonly_only="$1"
    local exec_id="$2"
    local timeout_s="${3:-300}"
    local deadline=$(( $(date +%s) + timeout_s ))
    local last_status=""
    while true; do
        if [[ $(date +%s) -gt $deadline ]]; then
            echo "错误: 等待执行 ${exec_id} 超时" >&2
            exit 2
        fi
        local resp status
        resp=$(curl_json GET "/executions/${exec_id}")
        status=$(printf '%s' "$resp" | jq -r '.status // empty')
        if [[ "$status" != "$last_status" ]]; then
            echo "Warden execution ${exec_id}: ${status:-UNKNOWN_STATUS}" >&2
            last_status="$status"
        fi
        case "$status" in
            SUCCEEDED|FAILED|CANCELLED|UNKNOWN|EXPIRED)
                echo "$resp"
                exit 0
                ;;
            WAITING_APPROVAL)
                if [[ -n "$readonly_only" ]]; then
                    echo "$resp"
                    echo "拒绝: 只读动作意外进入审批，请检查 Warden 策略，不自动继续" >&2
                    exit 3
                fi
                ;;
        esac
        sleep 2
    done
}

case "${1:-}" in
    create-execution)
        create_execution "" "$@"
        ;;
    create-readonly-execution)
        create_execution "readonly" "$@"
        ;;
    create-aiops-execution)
        create_execution "aiops" "$@"
        ;;
    get-execution)
        if [[ $# -lt 2 ]]; then
            echo "用法: warden-agent.sh get-execution <execution_id>" >&2
            exit 1
        fi
        curl_json GET "/executions/$2"
        echo
        ;;
    my-requests)
        status_filter="${2:-}"
        path="/my/access-requests"
        if [[ -n "$status_filter" ]]; then
            path="${path}?status=${status_filter}"
        fi
        curl_json GET "$path"
        echo
        ;;
    list-resources)
		type_filter="${2:-}"
		path="/resources?availableForExecution=true"
		if [[ -n "$type_filter" ]]; then
			path="${path}&type=${type_filter}"
		fi
		curl_json GET "$path"
		echo
		;;
	resource-summary)
		project_filter="${2:-}"
		environment_filter="${3:-}"
		type_filter="${4:-}"
		curl_json GET "/resources?availableForExecution=true" | jq \
			--arg project "$project_filter" --arg environment "$environment_filter" --arg type "$type_filter" '
			[.[] | select(($project == "" or .project_name == $project) and
			              ($environment == "" or .environment == $environment) and
			              ($type == "" or .resource_type == $type))]
			| sort_by(.project_name, .environment, .resource_type)
			| group_by([.project_name, .environment, .resource_type, .project_source, .environment_source])
			| map({project: (.[0].project_name // "未归属"), environment: .[0].environment,
			       resource_type: .[0].resource_type, project_source: .[0].project_source,
			       environment_source: .[0].environment_source, count: length})'
		echo
		;;
	find-resources)
		if [[ $# -lt 2 ]]; then
			echo "用法: warden-agent.sh find-resources <query> [project] [environment] [type]" >&2
			exit 1
		fi
		query="$2"
		project_filter="${3:-}"
		environment_filter="${4:-}"
		type_filter="${5:-}"
		curl_json GET "/resources?availableForExecution=true" | jq \
			--arg query "$query" --arg project "$project_filter" --arg environment "$environment_filter" --arg type "$type_filter" '
			[.[] | select(((.name // "") + " " + (.namespace // "") + " " + (.external_id // "")) | ascii_downcase | contains($query | ascii_downcase))
			      | select(($project == "" or .project_name == $project) and
			               ($environment == "" or .environment == $environment) and
			               ($type == "" or .resource_type == $type))
			      | {id, name, namespace, external_id, resource_type, status, project_id, project_name,
			         project_source, environment, environment_source, connector_id, integration_id}]'
		echo
		;;
	list-actions)
		resource_id="${2:-}"
		path="/actions"
		if [[ -n "$resource_id" ]]; then
			path="/resources/${resource_id}/actions"
		fi
		curl_json GET "$path"
		echo
		;;
    diagnose-context)
        if [[ $# -lt 2 ]]; then
            echo "用法: warden-agent.sh diagnose-context <json>" >&2
            exit 1
        fi
        curl_json POST /diagnostics -d "$2"
        echo
        ;;
    diagnose-result)
        if [[ $# -lt 2 ]]; then
            echo "用法: warden-agent.sh diagnose-result <traceId>" >&2
            exit 1
        fi
        curl_json GET "/diagnostics/$2"
        echo
        ;;
    wait-result)
        if [[ $# -lt 2 ]]; then
            echo "用法: warden-agent.sh wait-result <execution_id> [timeout_seconds]" >&2
            exit 1
        fi
        wait_execution "" "$2" "${3:-300}"
        ;;
    wait-readonly-result)
        if [[ $# -lt 2 ]]; then
            echo "用法: warden-agent.sh wait-readonly-result <execution_id> [timeout_seconds]" >&2
            exit 1
        fi
        wait_execution "readonly" "$2" "${3:-60}"
        ;;
    *)
        cat <<'USAGE' >&2
用法: warden-agent.sh <action> [args...]

子命令:
  create-execution <action> <resource_id> <params_json> [reason]
  create-readonly-execution <action> <resource_id> <params_json> [reason]
  create-aiops-execution <action> <resource_id> <params_json> [reason]
  get-execution <execution_id>
  list-resources [type]
  resource-summary [project] [environment] [type]
  find-resources <query> [project] [environment] [type]
  list-actions [resource_id]
  diagnose-context <json>
  diagnose-result <traceId>
  wait-result <execution_id> [timeout_seconds]
  wait-readonly-result <execution_id> [timeout_seconds]

环境变量:
  WARDEN_API_ENDPOINT     Warden API 地址 (必需)
  WARDEN_AGENT_API_KEY    API Key (与 warden-control 配置一致)
  WARDEN_ACTION_VERSION   显式覆盖 Action 版本；默认按动作选择当前版本
  WARDEN_TRACE_ID         可选会话 trace id（非空时并入 create-execution 请求）

依赖: curl, jq
USAGE
        exit 1
        ;;
esac
