package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

func (a *App) dispatch(evt FluxEvent) {
	if err := a.dispatchSlack(evt); err != nil {
		log.Printf("slack dispatch error: %v", err)
	}
	if err := a.dispatchGitHub(evt); err != nil {
		log.Printf("github dispatch error: %v", err)
	}
}

func (a *App) dispatchGitHub(evt FluxEvent) error {
	if !a.github.Enabled {
		log.Printf("github dispatch disabled")
		return nil
	}

	sha := evt.CommitSHA()
	if sha == "" {
		log.Printf("github dispatch skipped: no commit sha found in revision=%q", evt.Revision())
		return nil
	}

	statusPayload := map[string]any{
		"state":       githubState(evt),
		"context":     a.github.StatusContext,
		"description": truncate(singleLine(evt.Message), 140),
	}
	statusPretty, _ := json.MarshalIndent(statusPayload, "", "  ")

	if a.github.Token == "" || a.github.Repo == "" {
		log.Printf("github dry-run status repo=%q sha=%q payload:\n%s", a.github.Repo, sha, string(statusPretty))
		if a.github.PRComment {
			comment := buildGitHubComment(evt)
			commentPretty, _ := json.MarshalIndent(map[string]string{"body": comment}, "", "  ")
			log.Printf("github dry-run pr-comment repo=%q sha=%q payload:\n%s", a.github.Repo, sha, string(commentPretty))
		}
		return nil
	}

	statusURL := fmt.Sprintf("%s/repos/%s/statuses/%s", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, sha)
	headers := map[string]string{
		"Authorization":        "Bearer " + a.github.Token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
	if err := a.postJSON(statusURL, statusPayload, headers); err != nil {
		return err
	}

	if !a.github.PRComment {
		return nil
	}

	prs, err := a.fetchGitHubPullRequestsForCommit(sha)
	if err != nil {
		return err
	}
	for _, pr := range prs {
		commentBody := buildGitHubTrackingComment(pr, evt, a.github.FluxHubURL)
		if err := a.upsertGitHubTrackingComment(pr, commentBody, headers); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) fetchGitHubPullRequestsForCommit(sha string) ([]int, error) {
	url := fmt.Sprintf("%s/repos/%s/commits/%s/pulls", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, sha)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.github.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github fetch pulls failed status=%s body=%s", resp.Status, string(body))
	}

	var pulls []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(body, &pulls); err != nil {
		return nil, err
	}

	out := make([]int, 0, len(pulls))
	for _, pr := range pulls {
		out = append(out, pr.Number)
	}
	return out, nil
}

func (a *App) postJSON(url string, payload any, headers map[string]string) error {
	return a.sendJSON(http.MethodPost, url, payload, headers)
}

func (a *App) patchJSON(url string, payload any, headers map[string]string) error {
	return a.sendJSON(http.MethodPatch, url, payload, headers)
}

func (a *App) sendJSON(method, url string, payload any, headers map[string]string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed status=%s body=%s", method, url, resp.Status, string(respBody))
	}

	log.Printf("dispatch ok method=%s url=%s status=%s", method, url, resp.Status)
	return nil
}

func buildGitHubComment(evt FluxEvent) string {
	revision := evt.Revision()
	if revision == "" {
		revision = "n/a"
	}

	return fmt.Sprintf("## Flux deployment event\n\n- **Kind:** `%s`\n- **Namespace:** `%s`\n- **Name:** `%s`\n- **Severity:** `%s`\n- **Reason:** `%s`\n- **Controller:** `%s`\n- **Revision:** `%s`\n\n### Message\n\n```text\n%s\n```\n", evt.InvolvedObject.Kind, evt.InvolvedObject.Namespace, evt.InvolvedObject.Name, evt.Severity, evt.Reason, evt.ReportingController, revision, evt.Message)
}

const gitHubTrackingCommentMarker = "<!-- flux-hub:tracking-comment -->"

func buildGitHubTrackingComment(pr int, evt FluxEvent, fluxHubURL string) string {
	revision := evt.Revision()
	if revision == "" {
		revision = "n/a"
	}

	var linkLine string
	if fluxHubURL != "" {
		linkLine = fmt.Sprintf("Track rollout: %s/?pr=%d", fluxHubURL, pr)
	} else {
		linkLine = "Track rollout: set FLUX_HUB_URL to enable a clickable Flux Hub link"
	}

	return fmt.Sprintf("%s\n## Flux rollout tracking\n\n%s\n\nLatest signal:\n- **Kind:** `%s`\n- **Namespace:** `%s`\n- **Name:** `%s`\n- **Severity:** `%s`\n- **Reason:** `%s`\n- **Controller:** `%s`\n- **Revision:** `%s`\n\n### Message\n\n```text\n%s\n```\n", gitHubTrackingCommentMarker, linkLine, evt.InvolvedObject.Kind, evt.InvolvedObject.Namespace, evt.InvolvedObject.Name, evt.Severity, evt.Reason, evt.ReportingController, revision, evt.Message)
}

func (a *App) upsertGitHubTrackingComment(pr int, body string, headers map[string]string) error {
	commentID, err := a.findGitHubTrackingCommentID(pr, headers)
	if err != nil {
		return err
	}
	payload := map[string]string{"body": body}
	if commentID == 0 {
		commentURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, pr)
		return a.postJSON(commentURL, payload, headers)
	}
	commentURL := fmt.Sprintf("%s/repos/%s/issues/comments/%d", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, commentID)
	return a.patchJSON(commentURL, payload, headers)
}

func (a *App) findGitHubTrackingCommentID(pr int, headers map[string]string) (int64, error) {
	baseURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, pr)
	for page := 1; page <= 10; page++ {
		pageURL := baseURL + "&page=" + url.QueryEscape(fmt.Sprintf("%d", page))
		req, err := http.NewRequest(http.MethodGet, pageURL, nil)
		if err != nil {
			return 0, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return 0, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return 0, fmt.Errorf("github list issue comments failed status=%s body=%s", resp.Status, string(body))
		}
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(body, &comments); err != nil {
			return 0, err
		}
		if len(comments) == 0 {
			return 0, nil
		}
		for _, comment := range comments {
			if strings.Contains(comment.Body, gitHubTrackingCommentMarker) {
				return comment.ID, nil
			}
		}
		if len(comments) < 100 {
			return 0, nil
		}
	}
	return 0, nil
}

// ---------------------------------------------------------------------------
// PR / commit → HelmRelease resolution
// ---------------------------------------------------------------------------

// resolveHelmReleasesFromPR fetches the changed files for a GitHub PR, parses
// any YAML manifests that contain HelmRelease objects, and returns the refs.
func (a *App) resolveHelmReleasesFromPR(prNumber int) FocusResolution {
	rawParam := fmt.Sprintf("pr=%d", prNumber)
	if cached, ok := a.cachedFocusResolution(rawParam); ok {
		return cached
	}

	res := FocusResolution{
		RawParam: rawParam,
		Source:   fmt.Sprintf("PR #%d", prNumber),
	}

	headSHA, yamlFiles, err := a.fetchPRHeadAndYAMLFiles(prNumber)
	if err != nil {
		res.Error = err.Error()
		a.setCachedFocusResolution(rawParam, res)
		return res
	}
	res.HeadSHA = headSHA
	res.Files = yamlFiles

	targets, parseErr := a.helmReleasesFromFiles(headSHA, yamlFiles)
	if parseErr != "" {
		res.Error = parseErr
	}
	res.Targets = targets

	a.setCachedFocusResolution(rawParam, res)
	return res
}

// resolveHelmReleasesFromSHA fetches changed files for a commit SHA and
// parses HelmRelease manifests.
func (a *App) resolveHelmReleasesFromSHA(sha string) FocusResolution {
	rawParam := "sha=" + sha
	if cached, ok := a.cachedFocusResolution(rawParam); ok {
		return cached
	}

	res := FocusResolution{
		RawParam: rawParam,
		Source:   "commit " + shortSHA(sha),
		HeadSHA:  sha,
	}

	yamlFiles, err := a.fetchCommitYAMLFiles(sha)
	if err != nil {
		res.Error = err.Error()
		a.setCachedFocusResolution(rawParam, res)
		return res
	}
	res.Files = yamlFiles

	targets, parseErr := a.helmReleasesFromFiles(sha, yamlFiles)
	if parseErr != "" {
		res.Error = parseErr
	}
	res.Targets = targets

	a.setCachedFocusResolution(rawParam, res)
	return res
}

// fetchPRHeadAndYAMLFiles returns the head commit SHA and the list of
// changed .yaml/.yml file paths in the PR.
func (a *App) fetchPRHeadAndYAMLFiles(prNumber int) (headSHA string, files []string, err error) {
	// 1. Get head SHA from PR info.
	prURL := fmt.Sprintf("%s/repos/%s/pulls/%d", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, prNumber)
	var prInfo struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err = a.githubGet(prURL, &prInfo); err != nil {
		return "", nil, fmt.Errorf("fetch PR info: %w", err)
	}
	headSHA = prInfo.Head.SHA

	// 2. List changed files (paginated; cap at 300 files).
	filesURL := fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, prNumber)
	var filesList []struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
	}
	if err = a.githubGet(filesURL, &filesList); err != nil {
		return "", nil, fmt.Errorf("fetch PR files: %w", err)
	}

	for _, f := range filesList {
		if f.Status != "removed" && (strings.HasSuffix(f.Filename, ".yaml") || strings.HasSuffix(f.Filename, ".yml")) {
			files = append(files, f.Filename)
		}
	}
	return headSHA, files, nil
}

// fetchCommitYAMLFiles returns the list of changed .yaml/.yml paths for a commit.
func (a *App) fetchCommitYAMLFiles(sha string) ([]string, error) {
	commitURL := fmt.Sprintf("%s/repos/%s/commits/%s", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, sha)
	var commit struct {
		Files []struct {
			Filename string `json:"filename"`
			Status   string `json:"status"`
		} `json:"files"`
	}
	if err := a.githubGet(commitURL, &commit); err != nil {
		return nil, fmt.Errorf("fetch commit files: %w", err)
	}

	var files []string
	for _, f := range commit.Files {
		if f.Status != "removed" && (strings.HasSuffix(f.Filename, ".yaml") || strings.HasSuffix(f.Filename, ".yml")) {
			files = append(files, f.Filename)
		}
	}
	return files, nil
}

// helmReleasesFromFiles fetches each file's content at ref and parses
// HelmRelease manifests, returning the refs and any non-fatal error summary.
func (a *App) helmReleasesFromFiles(ref string, paths []string) ([]FluxObjectRef, string) {
	seen := map[string]bool{}
	var refs []FluxObjectRef
	var errs []string

	for _, path := range paths {
		content, err := a.fetchFileContentAtRef(ref, path)
		if err != nil {
			log.Printf("focus: fetch file %s@%s: %v", path, shortSHA(ref), err)
			errs = append(errs, fmt.Sprintf("could not fetch %s", path))
			continue
		}
		for _, hr := range helmReleasesFromYAML(content) {
			key := hr.Namespace + "/" + hr.Name
			if !seen[key] {
				seen[key] = true
				refs = append(refs, hr)
			}
		}
	}

	if len(errs) > 0 && len(refs) == 0 {
		return refs, strings.Join(errs, "; ")
	}
	return refs, ""
}

// fetchFileContentAtRef fetches raw file bytes at a given git ref via the
// GitHub contents API.
func (a *App) fetchFileContentAtRef(ref, path string) ([]byte, error) {
	u := fmt.Sprintf("%s/repos/%s/contents/%s?ref=%s",
		strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, path, ref)

	var resp struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := a.githubGet(u, &resp); err != nil {
		return nil, err
	}
	if resp.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected encoding %q", resp.Encoding)
	}
	// GitHub wraps base64 in newlines
	cleaned := strings.ReplaceAll(resp.Content, "\n", "")
	return base64.StdEncoding.DecodeString(cleaned)
}

// githubGet performs a GET request to the GitHub API with auth headers.
func (a *App) githubGet(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if a.github.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.github.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("github GET %s: %s — %s", url, resp.Status, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}

// helmReleasesFromYAML parses one file as multi-document YAML.
// Each YAML document is decoded independently; if a document has
// kind: HelmRelease and metadata.name, it contributes one {namespace, name}
// target. If metadata.namespace is absent, the caller later falls back to
// Kubernetes default namespace semantics.
func helmReleasesFromYAML(content []byte) []FluxObjectRef {
	type helmRelease struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}

	var refs []FluxObjectRef
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for {
		var doc helmRelease
		if err := decoder.Decode(&doc); err != nil {
			break // EOF or parse error — stop
		}
		if strings.EqualFold(doc.Kind, "HelmRelease") && doc.Metadata.Name != "" {
			refs = append(refs, FluxObjectRef{
				Kind:      "HelmRelease",
				Namespace: doc.Metadata.Namespace,
				Name:      doc.Metadata.Name,
			})
		}
	}
	return refs
}

func githubState(evt FluxEvent) string {
	if strings.EqualFold(evt.Severity, "error") {
		return "error"
	}
	if strings.EqualFold(evt.Severity, "info") {
		return "success"
	}
	return "failure"
}
