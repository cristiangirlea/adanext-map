package runner

import (
	"slices"
	"testing"
)

// The worker's environment loses what would bill per token and keeps the subscription's own
// login, which a headless CLI needs.
func TestScrubDropsProviderKeysAndKeepsTheSubscriptionLogin(t *testing.T) {
	got := scrub([]string{"PATH=/bin", "ANTHROPIC_API_KEY=a", "OPENAI_API_KEY=b", "CLAUDE_CODE_USE_BEDROCK=1",
		"ANTHROPIC_AUTH_TOKEN=c", "VENDOR_API_KEY=d", "CLAUDE_CODE_OAUTH_TOKEN=e", "HOME=/home/x"})
	want := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=e", "HOME=/home/x"}
	if !slices.Equal(got, want) {
		t.Fatalf("scrub = %q, want %q", got, want)
	}
}

// A gateway URL, a bearer token and the cloud providers' switches and credentials route a CLI to
// pay-per-token access as surely as an API key does.
func TestProviderKeysCoverGatewaysAndCloudRoutes(t *testing.T) {
	names := []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN", "AWS_PROFILE", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL",
		"ANTHROPIC_VERTEX_PROJECT_ID", "GOOGLE_APPLICATION_CREDENTIALS", "ANTHROPIC_FOUNDRY_BASE_URL",
		"ANTHROPIC_FOUNDRY_RESOURCE", "ANTHROPIC_FOUNDRY_API_KEY", "CLAUDE_CODE_SKIP_BEDROCK_AUTH",
		"CLAUDE_CODE_SKIP_VERTEX_AUTH", "CLAUDE_CODE_SKIP_FOUNDRY_AUTH"}
	var environ []string
	for _, n := range names {
		environ = append(environ, n+"=x")
	}
	got := ProviderKeysSet(environ)
	for _, n := range names {
		if !slices.Contains(got, n) {
			t.Errorf("%s is not refused", n)
		}
	}
	if kept := scrub(environ); len(kept) != 0 {
		t.Fatalf("scrub keeps %q", kept)
	}
}
