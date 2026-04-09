package main

import (
	"os"
	"strings"
)

type GitHubConfig struct {
	Enabled       bool
	Token         string
	Repo          string
	APIURL        string
	StatusContext string
	PRComment     bool
}

type SlackConfig struct {
	Enabled    bool
	WebhookURL string
}

func loadGitHubConfig() GitHubConfig {
	return GitHubConfig{
		Enabled:       envBoolDefault("GITHUB_ENABLED", true),
		Token:         os.Getenv("GITHUB_TOKEN"),
		Repo:          os.Getenv("GITHUB_REPO"),
		APIURL:        envOrDefault("GITHUB_API_URL", "https://api.github.com"),
		StatusContext: envOrDefault("GITHUB_STATUS_CONTEXT", "flux/deployment"),
		PRComment:     strings.EqualFold(envOrDefault("GITHUB_PR_COMMENT", "true"), "true"),
	}
}

func loadSlackConfig() SlackConfig {
	return SlackConfig{
		Enabled:    envBoolDefault("SLACK_ENABLED", false),
		WebhookURL: os.Getenv("SLACK_WEBHOOK_URL"),
	}
}
