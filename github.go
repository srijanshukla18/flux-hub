package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
	commentPayload := map[string]string{"body": buildGitHubComment(evt)}
	for _, pr := range prs {
		commentURL := fmt.Sprintf("%s/repos/%s/issues/%d/comments", strings.TrimRight(a.github.APIURL, "/"), a.github.Repo, pr)
		if err := a.postJSON(commentURL, commentPayload, headers); err != nil {
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
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
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
		return fmt.Errorf("post %s failed status=%s body=%s", url, resp.Status, string(respBody))
	}

	log.Printf("dispatch ok url=%s status=%s", url, resp.Status)
	return nil
}

func buildGitHubComment(evt FluxEvent) string {
	revision := evt.Revision()
	if revision == "" {
		revision = "n/a"
	}

	return fmt.Sprintf("## Flux deployment event\n\n- **Kind:** `%s`\n- **Namespace:** `%s`\n- **Name:** `%s`\n- **Severity:** `%s`\n- **Reason:** `%s`\n- **Controller:** `%s`\n- **Revision:** `%s`\n\n### Message\n\n```text\n%s\n```\n", evt.InvolvedObject.Kind, evt.InvolvedObject.Namespace, evt.InvolvedObject.Name, evt.Severity, evt.Reason, evt.ReportingController, revision, evt.Message)
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
