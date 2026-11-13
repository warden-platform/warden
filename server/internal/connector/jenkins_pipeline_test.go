package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJenkinsPipelineMetadataObservesSanitizedAssetFacts(t *testing.T) {
	const configXML = `<?xml version="1.0" encoding="UTF-8"?>
<flow-definition>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.ChoiceParameterDefinition>
          <name>ENV</name>
          <defaultValue>test</defaultValue>
          <choices class="java.util.Arrays$ArrayList"><a class="string-array"><string>test</string><string>production</string></a></choices>
        </hudson.model.ChoiceParameterDefinition>
        <hudson.model.StringParameterDefinition>
          <name>IMAGE_TAG</name>
          <defaultValue>v1.2.3</defaultValue>
        </hudson.model.StringParameterDefinition>
        <hudson.model.PasswordParameterDefinition>
          <name>API_TOKEN</name>
          <defaultValue>secret-default</defaultValue>
        </hudson.model.PasswordParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition">
    <scm class="hudson.plugins.git.GitSCM">
      <userRemoteConfigs>
        <hudson.plugins.git.UserRemoteConfig>
          <url>https://deploy-user:deploy-password@gitee.com/Example/Acme.git?token=hidden#main</url>
        </hudson.plugins.git.UserRemoteConfig>
      </userRemoteConfigs>
      <branches>
        <hudson.plugins.git.BranchSpec><name>*/test</name></hudson.plugins.git.BranchSpec>
      </branches>
    </scm>
    <scriptPath>ci/Jenkinsfile</scriptPath>
  </definition>
</flow-definition>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/acme-release/config.xml" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(configXML))
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{
		BaseURL: server.URL, Username: "agent", APIToken: "api-token", AllowedJobs: []string{"acme-release"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pipeline := runner.discoverJobPipelineMetadata(context.Background(), "acme-release")

	if pipeline["observation_status"] != "observed" {
		t.Fatalf("unexpected observation summary: %#v", pipeline)
	}
	if got := pipeline["repository_urls"]; !reflect.DeepEqual(got, []string{"https://gitee.com/Example/Acme"}) {
		t.Fatalf("unexpected sanitized repository URLs: %#v", got)
	}
	if got := pipeline["repository_keys"]; !reflect.DeepEqual(got, []string{"gitee.com/example/acme"}) {
		t.Fatalf("unexpected repository keys: %#v", got)
	}
	if got := pipeline["branch_specs"]; !reflect.DeepEqual(got, []string{"*/test"}) {
		t.Fatalf("unexpected branches: %#v", got)
	}
	if pipeline["jenkinsfile_path"] != "ci/Jenkinsfile" {
		t.Fatalf("incomplete pipeline observation: %#v", pipeline)
	}
	if got := pipeline["build_entrypoint"]; !reflect.DeepEqual(got, map[string]any{"type": "jenkinsfile", "path": "ci/Jenkinsfile"}) {
		t.Fatalf("unexpected build entrypoint: %#v", got)
	}
	parameters, ok := pipeline["parameters"].([]map[string]any)
	if !ok || len(parameters) != 3 {
		t.Fatalf("unexpected parameter metadata: %#v", pipeline["parameters"])
	}
	if parameters[0]["name"] != "API_TOKEN" || parameters[0]["default_redacted"] != true {
		t.Fatalf("sensitive default was not redacted: %#v", parameters[0])
	}
	if parameters[1]["name"] != "ENV" || parameters[1]["default_value"] != "test" || !reflect.DeepEqual(parameters[1]["choices"], []string{"test", "production"}) {
		t.Fatalf("safe choice metadata was not retained: %#v", parameters[1])
	}
	if parameters[2]["name"] != "IMAGE_TAG" || parameters[2]["default_value"] != "v1.2.3" {
		t.Fatalf("safe default was not retained: %#v", parameters[2])
	}
	for _, forbidden := range []string{"environment", "environment_confidence", "environment_evidence", "mapping_ready"} {
		if _, exists := pipeline[forbidden]; exists {
			t.Fatalf("neutral pipeline observation contains %q: %#v", forbidden, pipeline)
		}
	}

	encoded, err := json.Marshal(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	for _, secret := range []string{"deploy-user", "deploy-password", "token=hidden", "secret-default", "api-token"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("pipeline metadata leaked %q: %s", secret, serialized)
		}
	}
}

func TestJenkinsPipelineMetadataPrefersLastSuccessfulBuildWorkloadRepository(t *testing.T) {
	const configXML = `<?xml version="1.1" encoding="UTF-8"?>
<flow-definition>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <com.cwctravel.hudson.plugins.extended__choice__parameter.ExtendedChoiceParameterDefinition>
          <name>TRIGGER_DEPLOY_PROJECTS</name>
          <type>PT_CHECKBOX</type>
          <value>app-parent,app-agent</value>
          <defaultValue/>
        </com.cwctravel.hudson.plugins.extended__choice__parameter.ExtendedChoiceParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition">
    <scm class="hudson.plugins.git.GitSCM">
      <userRemoteConfigs><hudson.plugins.git.UserRemoteConfig><url>git@gitee.com:acme/devops.git</url></hudson.plugins.git.UserRemoteConfig></userRemoteConfigs>
      <branches><hudson.plugins.git.BranchSpec><name>*/master</name></hudson.plugins.git.BranchSpec></branches>
    </scm>
    <scriptPath>pipeline/pipeline_app.groovy</scriptPath>
  </definition>
</flow-definition>`
	const buildJSON = `{
  "number":2622,
  "actions":[
    {"remoteUrls":["git@gitee.com:acme/app.git"],"lastBuiltRevision":{"SHA1":"6951ccbc19b104db7d18e9ba025e35a0b1cd98da","branch":[{"name":"origin/master"}]}},
    {"remoteUrls":["git@gitee.com:acme/devops.git"],"lastBuiltRevision":{"SHA1":"7306ab6e5966e232466961ef116a206b9e582bbb","branch":[{"name":"origin/master"}]}}
  ]
}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/APP/job/app-k8s-prod/config.xml":
			_, _ = w.Write([]byte(configXML))
		case "/job/APP/job/app-k8s-prod/lastSuccessfulBuild/api/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(buildJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "api-token", AllowAll: true})
	if err != nil {
		t.Fatal(err)
	}
	pipeline := runner.discoverJobPipelineMetadata(context.Background(), "APP/app-k8s-prod")

	if got := pipeline["repository_keys"]; !reflect.DeepEqual(got, []string{"gitee.com/acme/app"}) {
		t.Fatalf("effective repositories = %#v, want only the runtime workload repository", got)
	}
	if got := pipeline["repository_urls"]; !reflect.DeepEqual(got, []string{"ssh://gitee.com/acme/app"}) {
		t.Fatalf("effective repository URLs = %#v", got)
	}
	if got := pipeline["branch_specs"]; !reflect.DeepEqual(got, []string{"origin/master"}) {
		t.Fatalf("effective branches = %#v", got)
	}
	if got := pipeline["definition_repository_keys"]; !reflect.DeepEqual(got, []string{"gitee.com/acme/devops"}) {
		t.Fatalf("definition repositories = %#v", got)
	}
	if got := pipeline["runtime_repository_keys"]; !reflect.DeepEqual(got, []string{"gitee.com/acme/devops", "gitee.com/acme/app"}) {
		t.Fatalf("runtime repositories = %#v", got)
	}
	if pipeline["repository_evidence_source"] != "last_successful_build" || pipeline["repository_observed_build"] != int64(2622) {
		t.Fatalf("runtime evidence provenance missing: %#v", pipeline)
	}
	parameters, ok := pipeline["parameters"].([]map[string]any)
	if !ok || len(parameters) != 1 || !reflect.DeepEqual(parameters[0]["choices"], []string{"app-parent", "app-agent"}) {
		t.Fatalf("extended choices were not retained: %#v", pipeline["parameters"])
	}
}

func TestEffectiveJenkinsBuildRepositoriesKeepsDefinitionWhenItIsTheOnlyRuntimeRepository(t *testing.T) {
	config := jenkinsPipelineConfig{DefinitionRepositoryKeys: []string{"gitee.com/acme/service"}}
	observed := []jenkinsBuildRepository{{Key: "gitee.com/acme/service", URL: "ssh://gitee.com/acme/service"}}
	if got := effectiveJenkinsBuildRepositories(config, observed); !reflect.DeepEqual(got, observed) {
		t.Fatalf("single-repository Pipeline SCM must remain publishable: %#v", got)
	}
}

func TestJenkinsPipelineConfigReadsDefaultsForLaterSafetyFiltering(t *testing.T) {
	config, err := parseJenkinsPipelineConfig([]byte(`<flow-definition><properties><hudson.model.ParametersDefinitionProperty><parameterDefinitions><hudson.model.StringParameterDefinition><name>ENV</name><defaultValue>production</defaultValue></hudson.model.StringParameterDefinition></parameterDefinitions></hudson.model.ParametersDefinitionProperty></properties><definition><scriptPath>Jenkinsfile</scriptPath></definition></flow-definition>`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.Parameters, []jenkinsParameterDefinition{{Name: "ENV", Type: "StringParameterDefinition", DefaultValue: "production", HasDefaultValue: true}}) {
		t.Fatalf("unexpected parameter facts: %#v", config.Parameters)
	}
}

func TestJenkinsPipelineConfigReadsSafeConfiguredBuildCommands(t *testing.T) {
	config, err := parseJenkinsPipelineConfig([]byte(`<project><builders><hudson.tasks.Shell><command>npm run build</command></hudson.tasks.Shell><hudson.tasks.Shell><command>docker build -t example/app .</command></hudson.tasks.Shell><hudson.tasks.Shell><command>deploy --token=should-not-return</command></hudson.tasks.Shell></builders></project>`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.BuildCommands, []string{"docker build -t example/app .", "npm run build"}) {
		t.Fatalf("unexpected safe build commands: %#v", config.BuildCommands)
	}
	if got := jenkinsBuildEntrypoint(config); !reflect.DeepEqual(got, map[string]any{
		"type": "configured_steps", "steps": []string{"Shell"},
		"commands": []string{"docker build -t example/app .", "npm run build"},
	}) {
		t.Fatalf("unexpected configured-step entrypoint: %#v", got)
	}
}

func TestJenkinsPipelineConfigAcceptsJenkinsXML11AndExtractsInlineRepositories(t *testing.T) {
	config, err := parseJenkinsPipelineConfig([]byte(`<?xml version='1.1' encoding='UTF-8'?>
<flow-definition>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.StringParameterDefinition>
          <name>FALLBACK_REPO_URL</name>
          <defaultValue>https://gitee.com/acme/fallback-service</defaultValue>
        </hudson.model.StringParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition">
    <script><![CDATA[
      git branch: 'test', credentialsId: 'gitee', url: 'git@gitee.com:acme/example-global-frontend.git'
      sh 'docker build -t swr.cn-east-3.myhuaweicloud.com/acme_public/example .'
      sh 'curl https://example.com/health'
    ]]></script>
  </definition>
</flow-definition>`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.RepositoryKeys, []string{
		"gitee.com/acme/fallback-service",
		"gitee.com/acme/example-global-frontend",
	}) {
		t.Fatalf("unexpected repository keys: %#v", config.RepositoryKeys)
	}
	if !reflect.DeepEqual(config.RepositoryURLs, []string{
		"https://gitee.com/acme/fallback-service",
		"ssh://gitee.com/acme/example-global-frontend",
	}) {
		t.Fatalf("unexpected repository URLs: %#v", config.RepositoryURLs)
	}
	if got := jenkinsBuildEntrypoint(config); !reflect.DeepEqual(got, map[string]any{"type": "inline_pipeline"}) {
		t.Fatalf("unexpected build entrypoint: %#v", got)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, falsePositive := range []string{"swr.cn-east-3.myhuaweicloud.com", "example.com/health", "credentialsId"} {
		if strings.Contains(string(encoded), falsePositive) {
			t.Fatalf("non-repository evidence %q was retained: %s", falsePositive, encoded)
		}
	}
}

func TestJenkinsPipelineConfigAcceptsDoubleQuotedXML11Declaration(t *testing.T) {
	config, err := parseJenkinsPipelineConfig([]byte(`<?xml version="1.1" encoding="UTF-8"?><project><scm><userRemoteConfigs><hudson.plugins.git.UserRemoteConfig><url>https://gitee.com/acme/api.git</url></hudson.plugins.git.UserRemoteConfig></userRemoteConfigs></scm></project>`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.RepositoryKeys, []string{"gitee.com/acme/api"}) {
		t.Fatalf("unexpected repository keys: %#v", config.RepositoryKeys)
	}
}

func TestJenkinsPipelineConfigStillRejectsMalformedXML11Content(t *testing.T) {
	if _, err := parseJenkinsPipelineConfig([]byte(`<?xml version="1.1"?><project><script>broken</project>`)); err == nil {
		t.Fatal("malformed XML must remain invalid after declaration normalization")
	}
}

func TestSafeJenkinsParameterValueRejectsSensitiveDefaults(t *testing.T) {
	tests := []struct{ name, parameterType, value string }{
		{name: "DEPLOY_TOKEN", parameterType: "StringParameterDefinition", value: "hidden"},
		{name: "TARGET", parameterType: "PasswordParameterDefinition", value: "hidden"},
		{name: "TARGET", parameterType: "StringParameterDefinition", value: "https://user:pass@example.com/release"},
		{name: "TARGET", parameterType: "StringParameterDefinition", value: "https://example.com/release?token=hidden"},
	}
	for _, item := range tests {
		if safeJenkinsParameterValue(item.name, item.parameterType, item.value) {
			t.Fatalf("sensitive default unexpectedly allowed: %#v", item)
		}
	}
	if !safeJenkinsParameterValue("IMAGE_TAG", "StringParameterDefinition", "v1.2.3") {
		t.Fatal("safe image tag default was rejected")
	}
}

func TestJenkinsPipelineMetadataMarksUnavailableConfig(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	runner, err := NewJenkinsRunner(JenkinsConfig{
		BaseURL: server.URL, Username: "agent", APIToken: "api-token", AllowedJobs: []string{"missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pipeline := runner.discoverJobPipelineMetadata(context.Background(), "missing")
	if !reflect.DeepEqual(pipeline, map[string]any{"observation_status": "unavailable"}) {
		t.Fatalf("unexpected unavailable metadata: %#v", pipeline)
	}
}

func TestJenkinsDiscoveryCompletenessDistinguishesFailureFromAuthorizedSnapshot(t *testing.T) {
	t.Run("root API failure is incomplete", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "api-token", AllowAll: true})
		if err != nil {
			t.Fatal(err)
		}
		resources, complete := runner.ListResourcesComplete(context.Background())
		if complete || len(resources) != 1 || resources[0].ResourceType != "jenkins_controller" {
			t.Fatalf("failed discovery should preserve an incomplete controller snapshot: complete=%v resources=%#v", complete, resources)
		}
	})

	t.Run("one visible job is a complete authorized snapshot", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/json":
				_, _ = w.Write([]byte(`{"jobs":[{"name":"only-visible-job","_class":"org.jenkinsci.plugins.workflow.job.WorkflowJob","color":"blue","buildable":true}]}`))
			case "/queue/api/json":
				_, _ = w.Write([]byte(`{"items":[]}`))
			case "/computer/api/json":
				_, _ = w.Write([]byte(`{"computer":[]}`))
			case "/credentials/store/system/domain/_/api/json":
				_, _ = w.Write([]byte(`{"credentials":[]}`))
			case "/job/only-visible-job/config.xml":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<flow-definition><definition class="org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition"><scriptPath>Jenkinsfile</scriptPath></definition></flow-definition>`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()
		runner, err := NewJenkinsRunner(JenkinsConfig{BaseURL: server.URL, Username: "agent", APIToken: "api-token", AllowAll: true})
		if err != nil {
			t.Fatal(err)
		}
		resources, complete := runner.ListResourcesComplete(context.Background())
		if !complete {
			t.Fatal("successful APIs with one authorized Job must be a complete snapshot")
		}
		jobs := 0
		for _, resource := range resources {
			if resource.ResourceType == "jenkins_job" {
				jobs++
			}
		}
		if jobs != 1 {
			t.Fatalf("unexpected visible Job count: %d (%#v)", jobs, resources)
		}
	})
}
