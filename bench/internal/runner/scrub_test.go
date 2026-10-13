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
