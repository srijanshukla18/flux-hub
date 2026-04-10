package app

import (
	"os"
	"strings"
)

type GitHubConfig struct {
	Enabled    bool
	Token      string
	Repo       string
	APIURL     string
	PRComment  bool
	FluxHubURL string
}

func loadGitHubConfig() GitHubConfig {
	return GitHubConfig{
		Enabled:    envBoolDefault("GITHUB_ENABLED", false),
		Token:      os.Getenv("GITHUB_TOKEN"),
		Repo:       os.Getenv("GITHUB_REPO"),
		APIURL:     envOrDefault("GITHUB_API_URL", "https://api.github.com"),
		PRComment:  strings.EqualFold(envOrDefault("GITHUB_PR_COMMENT", "false"), "true"),
		FluxHubURL: strings.TrimRight(os.Getenv("FLUX_HUB_URL"), "/"),
	}
}
