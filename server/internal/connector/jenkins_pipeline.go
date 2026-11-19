package connector

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const jenkinsPipelineObservationConcurrency = 6

var (
	jenkinsXML11VersionPattern      = regexp.MustCompile(`(?i)(<\?xml\b[^?>]*\bversion\s*=\s*["'])1\.1(["'])`)
	jenkinsScriptRepositoryPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:https?|ssh|git)://[^\s"'<>]+?\.git`),
		regexp.MustCompile(`(?i)(?:[a-z0-9._-]+@)?[a-z0-9.-]+\.[a-z]{2,}:[a-z0-9._~+%/-]+\.git`),
	}
)

type jenkinsParameterDefinition struct {
	Name            string
	Type            string
	DefaultValue    string
	HasDefaultValue bool
	Choices         []string
}

type jenkinsPipelineConfig struct {
	RepositoryURLs           []string
	RepositoryKeys           []string
	DefinitionRepositoryURLs []string
	DefinitionRepositoryKeys []string
	BranchSpecs              []string
	ScriptPath               string
	DefinitionType           string
	BuildStepTypes           []string
	BuildCommands            []string
	Parameters               []jenkinsParameterDefinition
}

type jenkinsBuildRepository struct {
	URL       string
	Key       string
	Branches  []string
	CommitSHA string
}

type jenkinsBuildRepositoryObservation struct {
	BuildNumber  int64
	Repositories []jenkinsBuildRepository
}

type jenkinsXMLFrame struct {
	name string
	text strings.Builder
}

func (r *JenkinsRunner) enrichJobPipelineMetadata(ctx context.Context, resources ...DiscoveredResource) []DiscoveredResource {
	type job struct {
		index int
		name  string
	}
	type result struct {
		index    int
		pipeline map[string]any
	}

	jobs := make(chan job)
	results := make(chan result)
	var workers sync.WaitGroup
	workerCount := min(jenkinsPipelineObservationConcurrency, len(resources))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				jobCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				pipeline := r.discoverJobPipelineMetadata(jobCtx, item.name)
				cancel()
				results <- result{index: item.index, pipeline: pipeline}
			}
		}()
	}
	go func() {
		for index := range resources {
			if resources[index].ResourceType == "jenkins_job" {
				jobs <- job{index: index, name: resources[index].Name}
			}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	for item := range results {
		if resources[item.index].Metadata == nil {
			resources[item.index].Metadata = map[string]any{}
		}
		resources[item.index].Metadata["jenkins_pipeline"] = item.pipeline
	}
	return resources
}

func (r *JenkinsRunner) enrichSingleJobPipelineMetadata(ctx context.Context, resource *DiscoveredResource) {
	if resource == nil || resource.ResourceType != "jenkins_job" {
		return
	}
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if resource.Metadata == nil {
		resource.Metadata = map[string]any{}
	}
	resource.Metadata["jenkins_pipeline"] = r.discoverJobPipelineMetadata(jobCtx, resource.Name)
}

func (r *JenkinsRunner) discoverJobPipelineMetadata(ctx context.Context, jobName string) map[string]any {
	data, err := r.do(ctx, http.MethodGet, jobPath(jobName)+"/config.xml", nil, "", jenkinsMaxResponse)
	if err != nil {
		return map[string]any{"observation_status": "unavailable"}
	}
	config, err := parseJenkinsPipelineConfig(data)
	if err != nil {
		return map[string]any{"observation_status": "invalid_config"}
	}
	parameters := make([]map[string]any, 0, len(config.Parameters))
	for _, parameter := range config.Parameters {
		if parameter.Name == "" {
			continue
		}
		item := map[string]any{"name": parameter.Name, "type": parameter.Type}
		if parameter.HasDefaultValue {
			if safeJenkinsParameterValue(parameter.Name, parameter.Type, parameter.DefaultValue) {
				item["default_value"] = parameter.DefaultValue
			} else {
				item["default_redacted"] = true
			}
		}
		choices := make([]string, 0, len(parameter.Choices))
		for _, choice := range parameter.Choices {
			if safeJenkinsParameterValue(parameter.Name, parameter.Type, choice) {
				choices = appendUnique(choices, choice)
			}
		}
		if len(choices) > 0 {
			item["choices"] = choices
		}
		parameters = append(parameters, item)
	}
	repositoryURLs := append([]string(nil), config.RepositoryURLs...)
	repositoryKeys := append([]string(nil), config.RepositoryKeys...)
	branchSpecs := append([]string(nil), config.BranchSpecs...)
	runtimeObservation, runtimeObserved := r.discoverLastSuccessfulBuildRepositories(ctx, jobName)
	if runtimeObserved && len(runtimeObservation.Repositories) > 0 {
		selected := effectiveJenkinsBuildRepositories(config, runtimeObservation.Repositories)
		if len(selected) > 0 {
			repositoryURLs, repositoryKeys, branchSpecs = repositoryFacts(selected)
		}
	}
	pipeline := map[string]any{
		"observation_status": "observed",
		"repository_urls":    repositoryURLs,
		"repository_keys":    repositoryKeys,
		"branch_specs":       branchSpecs,
		"parameters":         parameters,
	}
	if len(config.DefinitionRepositoryKeys) > 0 {
		pipeline["definition_repository_urls"] = config.DefinitionRepositoryURLs
		pipeline["definition_repository_keys"] = config.DefinitionRepositoryKeys
	}
	if runtimeObserved && len(runtimeObservation.Repositories) > 0 {
		runtimeURLs, runtimeKeys, _ := repositoryFacts(runtimeObservation.Repositories)
		pipeline["runtime_repository_urls"] = runtimeURLs
		pipeline["runtime_repository_keys"] = runtimeKeys
		pipeline["repository_evidence_source"] = "last_successful_build"
		pipeline["repository_observed_build"] = runtimeObservation.BuildNumber
	}
	if config.ScriptPath != "" {
		pipeline["jenkinsfile_path"] = config.ScriptPath
	}
	if entrypoint := jenkinsBuildEntrypoint(config); len(entrypoint) > 0 {
		pipeline["build_entrypoint"] = entrypoint
	}
	return pipeline
}

func (r *JenkinsRunner) discoverLastSuccessfulBuildRepositories(ctx context.Context, jobName string) (jenkinsBuildRepositoryObservation, bool) {
	var response struct {
		Number  int64 `json:"number"`
		Actions []struct {
			RemoteURLs        []string `json:"remoteUrls"`
			LastBuiltRevision struct {
				SHA1   string `json:"SHA1"`
				Branch []struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"lastBuiltRevision"`
		} `json:"actions"`
	}
	path := jobPath(jobName) + "/lastSuccessfulBuild/api/json?tree=number,actions[remoteUrls,lastBuiltRevision[SHA1,branch[name]]]"
	if err := r.getJSON(ctx, path, &response); err != nil || response.Number <= 0 {
		return jenkinsBuildRepositoryObservation{}, false
	}
	byKey := map[string]jenkinsBuildRepository{}
	for _, action := range response.Actions {
		branches := make([]string, 0, len(action.LastBuiltRevision.Branch))
		for _, branch := range action.LastBuiltRevision.Branch {
			branches = appendUnique(branches, branch.Name)
		}
		for _, rawURL := range action.RemoteURLs {
			safeURL, key, ok := normalizeJenkinsRepositoryURL(rawURL)
			if !ok {
				continue
			}
			observed := byKey[key]
			observed.URL = safeURL
			observed.Key = key
			for _, branch := range branches {
				observed.Branches = appendUnique(observed.Branches, branch)
			}
			if validGitObjectID(action.LastBuiltRevision.SHA1) {
				observed.CommitSHA = strings.ToLower(action.LastBuiltRevision.SHA1)
			}
			byKey[key] = observed
		}
	}
	repositories := make([]jenkinsBuildRepository, 0, len(byKey))
	for _, repository := range byKey {
		sort.Strings(repository.Branches)
		repositories = append(repositories, repository)
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Key < repositories[j].Key })
	return jenkinsBuildRepositoryObservation{BuildNumber: response.Number, Repositories: repositories}, true
}

func effectiveJenkinsBuildRepositories(config jenkinsPipelineConfig, observed []jenkinsBuildRepository) []jenkinsBuildRepository {
	definitionKeys := make(map[string]bool, len(config.DefinitionRepositoryKeys))
	for _, key := range config.DefinitionRepositoryKeys {
		definitionKeys[key] = true
	}
	workload := make([]jenkinsBuildRepository, 0, len(observed))
	for _, repository := range observed {
		if !definitionKeys[repository.Key] {
			workload = append(workload, repository)
		}
	}
	if len(workload) > 0 {
		return workload
	}
	return append([]jenkinsBuildRepository(nil), observed...)
}

func repositoryFacts(repositories []jenkinsBuildRepository) ([]string, []string, []string) {
	urls := make([]string, 0, len(repositories))
	keys := make([]string, 0, len(repositories))
	branches := []string{}
	for _, repository := range repositories {
		urls = appendUnique(urls, repository.URL)
		keys = appendUnique(keys, repository.Key)
		for _, branch := range repository.Branches {
			branches = appendUnique(branches, branch)
		}
	}
	sort.Strings(urls)
	sort.Strings(keys)
	sort.Strings(branches)
	return urls, keys, branches
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range strings.ToLower(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func parseJenkinsPipelineConfig(data []byte) (jenkinsPipelineConfig, error) {
	// Jenkins may serialize otherwise compatible Job configurations with an
	// XML 1.1 declaration. Go's encoding/xml rejects that declaration before
	// reading the first token, even when the document uses only XML 1.0-safe
	// characters. Normalize the declaration and still let the decoder reject
	// any genuinely unsupported XML 1.1 content later in the document.
	data = normalizeJenkinsConfigXMLVersion(data)
	decoder := xml.NewDecoder(bytes.NewReader(data))
	frames := make([]jenkinsXMLFrame, 0, 16)
	config := jenkinsPipelineConfig{}
	var parameter *jenkinsParameterDefinition
	parameterDepth := 0

	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return jenkinsPipelineConfig{}, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "definition" {
				for _, attribute := range value.Attr {
					if attribute.Name.Local == "class" {
						config.DefinitionType = shortJenkinsXMLType(attribute.Value)
						break
					}
				}
			}
			if len(frames) > 0 && frames[len(frames)-1].name == "builders" {
				config.BuildStepTypes = appendUnique(config.BuildStepTypes, shortJenkinsXMLType(value.Name.Local))
			}
			if len(frames) > 0 && frames[len(frames)-1].name == "parameterDefinitions" {
				parameter = &jenkinsParameterDefinition{Type: shortJenkinsXMLType(value.Name.Local)}
				parameterDepth = len(frames) + 1
			}
			frames = append(frames, jenkinsXMLFrame{name: value.Name.Local})
		case xml.CharData:
			if len(frames) > 0 {
				frames[len(frames)-1].text.Write([]byte(value))
			}
		case xml.EndElement:
			if len(frames) == 0 {
				continue
			}
			depth := len(frames)
			frame := frames[depth-1]
			text := strings.TrimSpace(frame.text.String())
			path := make([]string, 0, depth)
			for _, ancestor := range frames {
				path = append(path, ancestor.name)
			}
			switch {
			case frame.name == "url" && pathContains(path, "userRemoteConfigs"):
				if pathContains(path, "definition") {
					appendJenkinsDefinitionRepository(&config, text)
				} else {
					appendJenkinsRepository(&config, text)
				}
			case frame.name == "name" && pathContains(path, "branches"):
				config.BranchSpecs = appendUnique(config.BranchSpecs, text)
			case frame.name == "scriptPath":
				config.ScriptPath = text
			case frame.name == "script":
				appendJenkinsScriptRepositories(&config, text)
			case frame.name == "command" && pathContains(path, "builders"):
				appendJenkinsScriptRepositories(&config, text)
				if command, ok := safeJenkinsBuildCommand(text); ok {
					config.BuildCommands = appendUnique(config.BuildCommands, command)
				}
			}
			if parameter != nil && depth > parameterDepth && frame.name == "name" {
				parameter.Name = text
			}
			if parameter != nil && depth > parameterDepth && frame.name == "defaultValue" {
				parameter.DefaultValue = text
				parameter.HasDefaultValue = true
			}
			if parameter != nil && depth > parameterDepth && frame.name == "string" && pathContains(path, "choices") {
				parameter.Choices = appendUnique(parameter.Choices, text)
			}
			if parameter != nil && depth > parameterDepth && frame.name == "value" && strings.Contains(strings.ToLower(parameter.Type), "extendedchoice") {
				for _, choice := range strings.Split(text, ",") {
					parameter.Choices = appendUnique(parameter.Choices, choice)
				}
			}
			if parameter != nil && depth == parameterDepth {
				if parameter.HasDefaultValue && jenkinsRepositoryParameterName(parameter.Name) {
					appendJenkinsRepository(&config, parameter.DefaultValue)
				}
				config.Parameters = append(config.Parameters, *parameter)
				parameter = nil
				parameterDepth = 0
			}
			frames = frames[:depth-1]
		}
	}
	sort.Strings(config.RepositoryURLs)
	sort.Strings(config.RepositoryKeys)
	sort.Strings(config.DefinitionRepositoryURLs)
	sort.Strings(config.DefinitionRepositoryKeys)
	sort.Strings(config.BranchSpecs)
	sort.Strings(config.BuildStepTypes)
	sort.Strings(config.BuildCommands)
	sort.Slice(config.Parameters, func(i, j int) bool { return config.Parameters[i].Name < config.Parameters[j].Name })
	return config, nil
}

func appendJenkinsDefinitionRepository(config *jenkinsPipelineConfig, raw string) {
	if config == nil {
		return
	}
	if safeURL, key, ok := normalizeJenkinsRepositoryURL(raw); ok {
		config.RepositoryURLs = appendUnique(config.RepositoryURLs, safeURL)
		config.RepositoryKeys = appendUnique(config.RepositoryKeys, key)
		config.DefinitionRepositoryURLs = appendUnique(config.DefinitionRepositoryURLs, safeURL)
		config.DefinitionRepositoryKeys = appendUnique(config.DefinitionRepositoryKeys, key)
	}
}

func normalizeJenkinsConfigXMLVersion(data []byte) []byte {
	return jenkinsXML11VersionPattern.ReplaceAll(data, []byte("${1}1.0${2}"))
}

func appendJenkinsRepository(config *jenkinsPipelineConfig, raw string) {
	if config == nil {
		return
	}
	if safeURL, key, ok := normalizeJenkinsRepositoryURL(raw); ok {
		config.RepositoryURLs = appendUnique(config.RepositoryURLs, safeURL)
		config.RepositoryKeys = appendUnique(config.RepositoryKeys, key)
	}
}

func appendJenkinsScriptRepositories(config *jenkinsPipelineConfig, script string) {
	for _, pattern := range jenkinsScriptRepositoryPatterns {
		for _, candidate := range pattern.FindAllString(script, -1) {
			appendJenkinsRepository(config, candidate)
		}
	}
}

func jenkinsRepositoryParameterName(name string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"), ".", "_"))
	if normalized == "REPOSITORY" || normalized == "REPOSITORY_URL" || normalized == "REPO" || normalized == "REPO_URL" || normalized == "GIT_URL" || normalized == "GIT_REPOSITORY" {
		return true
	}
	return strings.HasSuffix(normalized, "_REPOSITORY_URL") || strings.HasSuffix(normalized, "_REPO_URL") || strings.HasSuffix(normalized, "_GIT_URL")
}

func jenkinsBuildEntrypoint(config jenkinsPipelineConfig) map[string]any {
	var entrypoint map[string]any
	if config.ScriptPath != "" {
		entrypoint = map[string]any{"type": "jenkinsfile", "path": config.ScriptPath}
	} else if strings.Contains(strings.ToLower(config.DefinitionType), "cpsflowdefinition") {
		entrypoint = map[string]any{"type": "inline_pipeline"}
	} else if len(config.BuildStepTypes) > 0 {
		entrypoint = map[string]any{"type": "configured_steps", "steps": config.BuildStepTypes}
	}
	if len(config.BuildCommands) > 0 {
		if entrypoint == nil {
			entrypoint = map[string]any{"type": "configured_steps"}
		}
		entrypoint["commands"] = config.BuildCommands
	}
	return entrypoint
}

func safeJenkinsBuildCommand(raw string) (string, bool) {
	command := strings.TrimSpace(raw)
	if command == "" || len(command) > 4096 || strings.ContainsAny(command, "\x00\r") {
		return "", false
	}
	lower := strings.ToLower(command)
	for _, marker := range []string{"password=", "passwd=", "secret=", "token=", "api_key=", "apikey=", "authorization:", "bearer ", "private_key"} {
		if strings.Contains(lower, marker) {
			return "", false
		}
	}
	return command, true
}

func safeJenkinsParameterValue(name, parameterType, value string) bool {
	upper := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"), ".", "_"))
	for _, marker := range []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "CREDENTIAL", "API_KEY", "APIKEY", "ACCESS_KEY", "ACCESSKEY", "PRIVATE_KEY", "PRIVATEKEY", "SIGNING_KEY", "COOKIE", "AUTHORIZATION"} {
		if strings.Contains(upper, marker) {
			return false
		}
	}
	if strings.Contains(strings.ToLower(parameterType), "password") || len(value) > 256 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"-----begin ", "password=", "passwd=", "secret=", "token=", "api_key=", "apikey=", "authorization:", "bearer "} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() && (parsed.User != nil || containsSensitiveQueryKey(parsed.Query())) {
		return false
	}
	return true
}

func containsSensitiveQueryKey(values url.Values) bool {
	for key := range values {
		upper := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
		for _, marker := range []string{"PASSWORD", "SECRET", "TOKEN", "API_KEY", "ACCESS_KEY", "CREDENTIAL"} {
			if strings.Contains(upper, marker) {
				return true
			}
		}
	}
	return false
}

func normalizeJenkinsRepositoryURL(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if !strings.Contains(raw, "://") {
		at := strings.LastIndex(raw, "@")
		colon := strings.Index(raw, ":")
		if colon > at+1 {
			host := strings.TrimSpace(raw[at+1 : colon])
			path := normalizeRepositoryPath(raw[colon+1:])
			if host != "" && path != "" {
				key := strings.ToLower(host) + "/" + strings.ToLower(path)
				return "ssh://" + host + "/" + path, key, true
			}
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	path := normalizeRepositoryPath(parsed.Path)
	if path == "" {
		return "", "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ssh" && scheme != "git" {
		scheme = "https"
	}
	return scheme + "://" + host + "/" + path, host + "/" + strings.ToLower(path), true
}

func normalizeRepositoryPath(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	return strings.Trim(path, "/")
}

func shortJenkinsXMLType(value string) string {
	if index := strings.LastIndex(value, "."); index >= 0 {
		value = value[index+1:]
	}
	return strings.TrimSpace(value)
}

func pathContains(path []string, expected string) bool {
	for _, item := range path {
		if item == expected {
			return true
		}
	}
	return false
}

func appendUnique(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
