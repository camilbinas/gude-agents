package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/camilbinas/gude-agents/agent/tool"
)

// ── Mock data ─────────────────────────────────────────────────────────────────
// In a real system these would come from GitHub/GitLab APIs, a database, etc.

type repo struct {
	name        string
	language    string
	description string
	openPRs     int
	openIssues  int
	lastCommit  string
}

type pullRequest struct {
	id       int
	repo     string
	title    string
	author   string
	status   string // open, merged, closed
	comments int
}

type teamMember struct {
	name    string
	role    string
	repos   []string
	openPRs int
}

var repos = []repo{
	{name: "api-gateway", language: "Go", description: "Central HTTP gateway, routing, and auth middleware", openPRs: 3, openIssues: 7, lastCommit: "2h ago"},
	{name: "web-app", language: "TypeScript", description: "React frontend for the main product", openPRs: 5, openIssues: 12, lastCommit: "45m ago"},
	{name: "data-pipeline", language: "Python", description: "ETL jobs and data processing workers", openPRs: 1, openIssues: 4, lastCommit: "1d ago"},
	{name: "mobile-app", language: "Swift", description: "iOS client application", openPRs: 2, openIssues: 9, lastCommit: "3h ago"},
	{name: "infra", language: "HCL", description: "Terraform modules for AWS infrastructure", openPRs: 0, openIssues: 2, lastCommit: "4d ago"},
}

var pullRequests = []pullRequest{
	{id: 441, repo: "api-gateway", title: "Add rate limiting middleware", author: "sara", status: "open", comments: 4},
	{id: 442, repo: "api-gateway", title: "Refactor auth token validation", author: "tom", status: "open", comments: 2},
	{id: 443, repo: "api-gateway", title: "Fix nil pointer in route handler", author: "sara", status: "open", comments: 1},
	{id: 201, repo: "web-app", title: "Dashboard redesign", author: "mia", status: "open", comments: 8},
	{id: 202, repo: "web-app", title: "Add dark mode toggle", author: "tom", status: "open", comments: 3},
	{id: 203, repo: "web-app", title: "Fix mobile layout breakpoints", author: "mia", status: "open", comments: 0},
	{id: 204, repo: "web-app", title: "Upgrade React to v19", author: "alex", status: "open", comments: 6},
	{id: 205, repo: "web-app", title: "Remove deprecated API calls", author: "alex", status: "open", comments: 1},
	{id: 88, repo: "data-pipeline", title: "Parallelize ingestion workers", author: "alex", status: "open", comments: 5},
	{id: 71, repo: "mobile-app", title: "Push notification support", author: "mia", status: "open", comments: 2},
	{id: 72, repo: "mobile-app", title: "Fix crash on deep link open", author: "tom", status: "open", comments: 7},
}

var team = []teamMember{
	{name: "sara", role: "backend engineer", repos: []string{"api-gateway", "data-pipeline"}, openPRs: 2},
	{name: "tom", role: "fullstack engineer", repos: []string{"api-gateway", "web-app", "mobile-app"}, openPRs: 3},
	{name: "mia", role: "frontend engineer", repos: []string{"web-app", "mobile-app"}, openPRs: 3},
	{name: "alex", role: "backend engineer", repos: []string{"web-app", "data-pipeline"}, openPRs: 3},
}

// ── Tools ─────────────────────────────────────────────────────────────────────

// searchReposTool searches repos by name or language.
func searchReposTool() tool.Tool {
	type Input struct {
		Query string `json:"query" description:"Search term to match against repo name, language, or description" required:"true"`
	}
	return tool.New("search_repos",
		"Search repositories by name, language, or description keyword.",
		func(_ context.Context, in Input) (string, error) {
			q := strings.ToLower(in.Query)
			var matches []repo
			for _, r := range repos {
				if strings.Contains(strings.ToLower(r.name), q) ||
					strings.Contains(strings.ToLower(r.language), q) ||
					strings.Contains(strings.ToLower(r.description), q) {
					matches = append(matches, r)
				}
			}
			if len(matches) == 0 {
				return fmt.Sprintf("No repos found for %q.", in.Query), nil
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "Found %d repo(s):\n", len(matches))
			for _, r := range matches {
				fmt.Fprintf(&sb, "- %s [%s] — %s | PRs: %d | Issues: %d | Last commit: %s\n",
					r.name, r.language, r.description, r.openPRs, r.openIssues, r.lastCommit)
			}
			return sb.String(), nil
		},
	)
}

// searchPRsTool searches open pull requests by repo or author.
func searchPRsTool() tool.Tool {
	type Input struct {
		Repo   string `json:"repo"   description:"Filter by repository name"`
		Author string `json:"author" description:"Filter by PR author name"`
	}
	return tool.New("search_prs",
		"Search open pull requests, optionally filtered by repo or author.",
		func(_ context.Context, in Input) (string, error) {
			var matches []pullRequest
			for _, pr := range pullRequests {
				if in.Repo != "" && !strings.Contains(pr.repo, strings.ToLower(in.Repo)) {
					continue
				}
				if in.Author != "" && !strings.Contains(pr.author, strings.ToLower(in.Author)) {
					continue
				}
				matches = append(matches, pr)
			}
			if len(matches) == 0 {
				return "No matching pull requests found.", nil
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "Found %d PR(s):\n", len(matches))
			for _, pr := range matches {
				fmt.Fprintf(&sb, "- #%d [%s] %s — by %s | %d comment(s)\n",
					pr.id, pr.repo, pr.title, pr.author, pr.comments)
			}
			return sb.String(), nil
		},
	)
}

// searchTeamTool looks up team members by name or role.
func searchTeamTool() tool.Tool {
	type Input struct {
		Query string `json:"query" description:"Search by name or role (e.g. 'backend', 'sara')" required:"true"`
	}
	return tool.New("search_team",
		"Look up team members by name or role. Returns their repos and open PR count.",
		func(_ context.Context, in Input) (string, error) {
			q := strings.ToLower(in.Query)
			var matches []teamMember
			for _, m := range team {
				if strings.Contains(m.name, q) || strings.Contains(m.role, q) {
					matches = append(matches, m)
				}
			}
			if len(matches) == 0 {
				return fmt.Sprintf("No team members found for %q.", in.Query), nil
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "Found %d member(s):\n", len(matches))
			for _, m := range matches {
				fmt.Fprintf(&sb, "- %s (%s) | repos: %s | open PRs: %d\n",
					m.name, m.role, strings.Join(m.repos, ", "), m.openPRs)
			}
			return sb.String(), nil
		},
	)
}
