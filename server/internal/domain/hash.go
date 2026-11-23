package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func HashParameters(params map[string]any) string {
	h := sha256.New()
	h.Write([]byte(flatten(params)))
	return hex.EncodeToString(h.Sum(nil))
}

func HashScope(principalID, resourceID, actionID, paramHash string, actingProjectID ...string) string {
	h := sha256.New()
	projectID := ""
	if len(actingProjectID) > 0 {
		projectID = actingProjectID[0]
	}
	payload := fmt.Sprintf("%s|%s|%s|%s", principalID, resourceID, actionID, paramHash)
	if projectID != "" {
		payload += "|" + projectID
	}
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

func NewTraceID() string {
	h := sha256.New()
	h.Write([]byte(time.Now().String()))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// HashDispatchToken 对任务分发令牌做不可逆哈希，落库存哈希而非明文，防止
// 拥有库只读权限者可回放 dispatch_token 伪造执行结果。
func HashDispatchToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func flatten(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+":"+flatten(val[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		var parts []string
		for _, item := range val {
			parts = append(parts, flatten(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
