package control

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestAuditActionsCoveredByEnum 根治「新增审计动作漏配迁移」：静态扫描代码里
// 所有审计动作字面量，断言都存在于 migrations 的 audit_action 枚举值集合。
// 不依赖数据库，纯文件静态校验，可进 CI（无 DB 也能跑）。
func TestAuditActionsCoveredByEnum(t *testing.T) {
	enum := parseAuditActionEnum(t)
	actions := scanAuditActionLiterals(t)
	var missing []string
	for _, a := range actions {
		if !enum[a] {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("以下审计动作未加入 audit_action 枚举，需补一条 ALTER TYPE 迁移：%s", strings.Join(missing, ", "))
	}
}

// 审计动作字面量来源分两类：
//   直接写入（实参恒为审计动作）：writeAudit / executionEvent / executionAuditEvent
//   Action 字段：domain.AuditEvent 与 connector HelperRequest 都使用，需域名过滤。
var (
	directActionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`writeAudit\([^,]*,\s*"([a-z_][a-z0-9_.]*)"`),
		regexp.MustCompile(`executionEvent\(\s*"([a-z_][a-z0-9_.]*)"`),
		regexp.MustCompile(`executionAuditEvent\(\s*"([a-z_][a-z0-9_.]*)"`),
	}
	actionFieldPattern = regexp.MustCompile(`Action:\s*"([a-z_][a-z0-9_.]*)"`)
	auditActionDomainRe = regexp.MustCompile(`^(execution|access_request|grant|connector|integration|policy|credential|account|project|principal|delegation|external_binding|federated|notification|permission_drift|provider_capability|resource|user|authorization|network_connector)\.`)
)

func scanAuditActionLiterals(t *testing.T) []string {
	t.Helper()
	internalDir := filepath.Join("..", "..", "internal")
	seen := map[string]bool{}
	var result []string
	add := func(action string) {
		if !seen[action] {
			seen[action] = true
			result = append(result, action)
		}
	}
	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			for _, re := range directActionPatterns {
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					add(m[1])
				}
			}
			for _, m := range actionFieldPattern.FindAllStringSubmatch(line, -1) {
				if auditActionDomainRe.MatchString(m[1]) {
					add(m[1])
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("扫描内部代码失败: %v", err)
	}
	sort.Strings(result)
	return result
}

// parseAuditActionEnum 从 migrations 里提取 audit_action 枚举的完整值集合：
// 001 的 CREATE TYPE AS ENUM(...) 以及各迁移的 ALTER TYPE ... ADD VALUE 'x'。
func parseAuditActionEnum(t *testing.T) map[string]bool {
	t.Helper()
	migrationsDir := filepath.Join("..", "..", "migrations")
	enum := map[string]bool{}
	enumRe := regexp.MustCompile(`(?s)CREATE TYPE audit_action AS ENUM\s*\((.*?)\)`)
	addValueRe := regexp.MustCompile(`ADD VALUE (?:IF NOT EXISTS )?'([a-z_][a-z0-9_.]*)'`)
	valueRe := regexp.MustCompile(`'([a-z_][a-z0-9_.]*)'`)

	err := filepath.Walk(migrationsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(b)
		// 001 的枚举定义
		if m := enumRe.FindStringSubmatch(content); m != nil {
			for _, v := range valueRe.FindAllStringSubmatch(m[1], -1) {
				enum[v[1]] = true
			}
		}
		// 后续迁移的 ADD VALUE
		for _, m := range addValueRe.FindAllStringSubmatch(content, -1) {
			enum[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 migrations 失败: %v", err)
	}
	if len(enum) == 0 {
		t.Fatal("未解析到任何 audit_action 枚举值，检查 migrations 路径")
	}
	return enum
}