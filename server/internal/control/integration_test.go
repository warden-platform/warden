package control

import "testing"

func TestValidateIntegrationH3C(t *testing.T) {
	valid := integrationInput{Protocol: "h3c_comware_ssh", Name: "core-01", Endpoint: "ssh://192.0.2.10:22", Username: "netops", CredentialRef: "h3c-core-key", Scope: map[string]any{
		"deviceRole": "core", "hostKeySHA256": "SHA256:abcdefghijklmnop=", "legacyRsa": false,
		"allowInterfaceChanges": true, "protectedInterfaces": []any{"Bridge-Aggregation1", "XGE1/0/49"},
	}}
	if err := validateIntegration(valid); err != nil {
		t.Fatalf("valid H3C integration rejected: %v", err)
	}
	for _, mutate := range []func(*integrationInput){
		func(in *integrationInput) { in.Endpoint = "ssh://switch.example:22" },
		func(in *integrationInput) { in.Scope["hostKeySHA256"] = "" },
		func(in *integrationInput) { in.Scope["deviceRole"] = "firewall" },
		func(in *integrationInput) { in.Username = "net ops" },
		func(in *integrationInput) { in.Scope["allowInterfaceChanges"] = "yes" },
		func(in *integrationInput) { in.Scope["protectedInterfaces"] = []any{"../../bad"} },
	} {
		input := valid
		input.Scope = map[string]any{"deviceRole": "core", "hostKeySHA256": "SHA256:abcdefghijklmnop=", "legacyRsa": false}
		mutate(&input)
		if err := validateIntegration(input); err == nil {
			t.Fatalf("invalid H3C integration was accepted: %#v", input)
		}
	}
}

func TestValidateIntegrationJenkins(t *testing.T) {
	valid := integrationInput{
		Protocol: "jenkins", Name: "prod-jenkins", Endpoint: "https://jenkins.example.test",
		Username: "warden-connector", CredentialRef: "jenkins-api-token",
		Scope: map[string]any{"allowedJobs": []any{"team/deploy", "platform/*"}},
	}
	if err := validateIntegration(valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*integrationInput)
	}{
		{"embedded endpoint credential", func(in *integrationInput) { in.Endpoint = "https://token@jenkins.example.test" }},
		{"global job wildcard", func(in *integrationInput) { in.Scope = map[string]any{"allowedJobs": []any{"*"}} }},
		{"invalid credential reference", func(in *integrationInput) { in.CredentialRef = "../../token" }},
		{"empty name", func(in *integrationInput) { in.Name = "" }},
		{"unsupported protocol", func(in *integrationInput) { in.Protocol = "redis" }},
		{"missing username", func(in *integrationInput) { in.Username = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if err := validateIntegration(input); err == nil {
				t.Fatal("invalid Integration config was accepted")
			}
		})
	}
}

func TestValidateIntegrationEnvironment(t *testing.T) {
	base := integrationInput{
		Protocol: "jenkins", Name: "shared-jenkins", Endpoint: "https://jenkins.example.test",
		Username: "warden-connector", CredentialRef: "jenkins-api-token", Scope: map[string]any{"allowAll": true},
	}
	for _, environment := range []string{"", "development", "testing", "production"} {
		input := base
		input.Environment = environment
		if err := validateIntegration(input); err != nil {
			t.Fatalf("valid environment %q rejected: %v", environment, err)
		}
	}
	base.Environment = "staging"
	if err := validateIntegration(base); err == nil {
		t.Fatal("unsupported environment was accepted")
	}
}

func TestValidateIntegrationDisabledJenkinsKeepsRequiredFields(t *testing.T) {
	// Unlike the legacy adapter (which allowed Enabled=false to skip fields),
	// integrations are always concrete: a create must carry a full endpoint.
	input := integrationInput{
		Protocol: "jenkins", Name: "prod-jenkins", Endpoint: "https://jenkins.example.test",
		Username: "warden-connector", CredentialRef: "jenkins-api-token",
		Scope: map[string]any{"allowedJobs": []any{"team/deploy"}},
	}
	if err := validateIntegration(input); err != nil {
		t.Fatalf("valid integration rejected: %v", err)
	}
}

func TestValidateIntegrationJenkinsRequiresExplicitScope(t *testing.T) {
	input := integrationInput{
		Protocol: "jenkins", Name: "prod-jenkins", Endpoint: "https://jenkins.example.test",
		Username: "warden-connector", CredentialRef: "jenkins-api-token",
		Scope: map[string]any{},
	}
	if err := validateIntegration(input); err == nil {
		t.Fatal("empty Job scope was accepted")
	}
	input.Scope = map[string]any{"allowAll": true}
	if err := validateIntegration(input); err != nil {
		t.Fatalf("explicit allowAll was rejected: %v", err)
	}
}

func TestBuildIntegrationPreservesExplicitScope(t *testing.T) {
	tests := []struct {
		protocol string
		scope    map[string]any
		key      string
	}{
		{"jenkins", map[string]any{"allowAll": true}, "allowAll"},
		{"grafana", map[string]any{"allowAll": true}, "allowAll"},
		{"postgresql", map[string]any{"allowedSchemas": []any{"public"}}, "allowedSchemas"},
		{"huaweicloud", map[string]any{"allowAll": true, "regionPolicy": map[string]any{"mode": "all", "regions": []any{}, "deniedRegions": []any{"cn-north-9"}}}, "regionPolicy"},
		{"aliyun_sls", map[string]any{"allowAll": true, "regionPolicy": map[string]any{"mode": "all", "regions": []any{}, "deniedRegions": []any{"us-east-1"}}}, "regionPolicy"},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			integration := buildIntegration("connector", integrationInput{Protocol: test.protocol, Scope: test.scope})
			if _, ok := integration.Scope[test.key]; !ok {
				t.Fatalf("scope %q was not preserved: %#v", test.key, integration.Scope)
			}
		})
	}
}

func TestValidateIntegrationAliyunSLS(t *testing.T) {
	valid := integrationInput{Protocol: "aliyun_sls", Name: "aipc-prod-logs", Endpoint: "cn-hangzhou", CredentialRef: "aliyun-sls-ak-sk", Scope: map[string]any{"allowAll": true, "regionPolicy": map[string]any{"mode": "all", "regions": []any{}, "deniedRegions": []any{}}}}
	if err := validateIntegration(valid); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"hangzhou", "http://aipc-log.cn-hangzhou.log.aliyuncs.com", "https://cn-hangzhou.log.aliyuncs.com", "https://token@aipc-log.cn-hangzhou.log.aliyuncs.com", "https://example.com"} {
		input := valid
		input.Endpoint = endpoint
		if err := validateIntegration(input); err == nil {
			t.Fatalf("invalid SLS endpoint accepted: %s", endpoint)
		}
	}
	valid.Scope = map[string]any{}
	if err := validateIntegration(valid); err == nil {
		t.Fatal("SLS Integration without explicit scope was accepted")
	}
	legacy := valid
	legacy.Endpoint = "https://aipc-log.cn-hangzhou.log.aliyuncs.com"
	legacy.Scope = map[string]any{"allowAll": true}
	if err := validateIntegration(legacy); err != nil {
		t.Fatalf("legacy project endpoint was not preserved: %v", err)
	}
}

func TestBuildHuaweiIntegrationPreservesRegionPolicy(t *testing.T) {
	integration := buildIntegration("connector", integrationInput{
		Protocol: "huaweicloud",
		Scope: map[string]any{
			"allowAll": true,
			"regionPolicy": map[string]any{
				"mode": "all", "regions": []any{}, "deniedRegions": []any{"cn-north-9"},
			},
		},
	})
	policy, ok := integration.Scope["regionPolicy"].(map[string]any)
	if !ok || policy["mode"] != "all" {
		t.Fatalf("region policy was not preserved: %#v", integration.Scope)
	}
	denied, ok := policy["deniedRegions"].([]string)
	if !ok || len(denied) != 1 || denied[0] != "cn-north-9" {
		t.Fatalf("denied regions were not preserved: %#v", policy)
	}
}

func TestValidateIntegrationGrafana(t *testing.T) {
	valid := integrationInput{
		Protocol: "grafana", Name: "prod-grafana", Endpoint: "https://grafana.example.test",
		CredentialRef: "grafana-sa-token",
		Scope:         map[string]any{"allowedDashboards": []any{"ops-overview", "latency-dashboard"}},
	}
	if err := validateIntegration(valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*integrationInput)
	}{
		{"embedded endpoint credential", func(in *integrationInput) { in.Endpoint = "https://token@grafana.example.test" }},
		{"endpoint with query", func(in *integrationInput) { in.Endpoint = "https://grafana.example.test?foo=bar" }},
		{"invalid dashboard UID", func(in *integrationInput) { in.Scope = map[string]any{"allowedDashboards": []any{"bad uid!"}} }},
		{"invalid credential reference", func(in *integrationInput) { in.CredentialRef = "../../token" }},
		{"empty name", func(in *integrationInput) { in.Name = "" }},
		{"unsupported protocol", func(in *integrationInput) { in.Protocol = "redis" }},
		{"too many dashboards", func(in *integrationInput) {
			uids := make([]any, 201)
			for i := range uids {
				uids[i] = "dash"
			}
			in.Scope = map[string]any{"allowedDashboards": uids}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if err := validateIntegration(input); err == nil {
				t.Fatal("invalid Grafana Integration config was accepted")
			}
		})
	}
}

func TestValidateIntegrationGrafanaRequiresExplicitScope(t *testing.T) {
	input := integrationInput{
		Protocol: "grafana", Name: "prod-grafana", Endpoint: "https://grafana.example.test",
		CredentialRef: "grafana-sa-token",
		Scope:         map[string]any{},
	}
	if err := validateIntegration(input); err == nil {
		t.Fatal("empty Dashboard scope was accepted")
	}
	input.Scope = map[string]any{"allowAll": true}
	if err := validateIntegration(input); err != nil {
		t.Fatalf("explicit allowAll was rejected: %v", err)
	}
}

func TestValidateIntegrationDatabases(t *testing.T) {
	for _, input := range []integrationInput{
		{Protocol: "postgresql", Name: "orders-db", Endpoint: "postgresql://db.example.test:5432/orders?sslmode=require", Username: "warden_reader", CredentialRef: "orders-db-password", Scope: map[string]any{"allowedSchemas": []any{"public"}}},
		{Protocol: "mysql", Name: "inventory-db", Endpoint: "mysql://db.example.test:3306/inventory?tls=true", Username: "warden_reader", CredentialRef: "inventory-db-password", Scope: map[string]any{"allowAll": true}},
	} {
		if err := validateIntegration(input); err != nil {
			t.Fatalf("valid %s integration rejected: %v", input.Protocol, err)
		}
	}

	invalid := []integrationInput{
		{Protocol: "postgresql", Name: "db", Endpoint: "postgresql://user:pass@db.example.test/orders", Username: "reader", CredentialRef: "db-password"},
		{Protocol: "mysql", Name: "db", Endpoint: "mysql://db.example.test/inventory?tls=skip-verify", Username: "reader", CredentialRef: "db-password"},
		{Protocol: "mysql", Name: "db", Endpoint: "mysql://db.example.test/inventory", Username: "", CredentialRef: "db-password"},
	}
	for _, input := range invalid {
		if err := validateIntegration(input); err == nil {
			t.Fatalf("invalid %s integration was accepted: %#v", input.Protocol, input)
		}
	}
}

func TestValidateIntegrationHuaweiCloud(t *testing.T) {
	valid := integrationInput{
		Protocol: "huaweicloud", Name: "prod-huawei", Endpoint: "cn-north-4",
		Username: "warden-readonly", CredentialRef: "huawei-ak-sk",
		Scope: map[string]any{"allowedServices": []any{"ecs", "OBS", "swr"}},
	}
	if err := validateIntegration(valid); err != nil {
		t.Fatal(err)
	}
	built := buildIntegration("conn-1", valid)
	services, _ := built.Scope["allowedServices"].([]string)
	if len(services) != 3 || services[1] != "obs" || services[2] != "swr" {
		t.Fatalf("service scope was not normalized to lowercase: %#v", built.Scope)
	}

	tests := []struct {
		name   string
		mutate func(*integrationInput)
	}{
		{"endpoint is a URL not a region", func(in *integrationInput) { in.Endpoint = "https://ecs.cn-north-4.myhuaweicloud.com" }},
		{"unknown service scope", func(in *integrationInput) { in.Scope = map[string]any{"allowedServices": []any{"redis"}} }},
		{"empty scope without allowAll", func(in *integrationInput) { in.Scope = map[string]any{} }},
		{"invalid credential reference", func(in *integrationInput) { in.CredentialRef = "../../aksk" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if err := validateIntegration(input); err == nil {
				t.Fatal("invalid Huawei Cloud Integration config was accepted")
			}
		})
	}
	valid.Scope = map[string]any{"allowAll": true}
	if err := validateIntegration(valid); err != nil {
		t.Fatalf("allowAll scope rejected: %v", err)
	}
}
