// Package runner executes a benchmark manifest's plan, one fresh workspace per run, through an
// adapter per coding CLI (claude first, and a fake one for tests and synthetic fixtures). Its real
// execution path refuses to start unless the approval matches the plan and no provider API key is
// set; DryRun prints every planned cell and runs nothing.
package runner

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// ApprovalFlag is the flag a real run needs, set to the plan's run count.
const ApprovalFlag = "--i-have-approval-for-N-runs"

// ProviderKeysSet lists, sorted, the variables in environ that would hand a provider API key or a
// pay-per-token route to a CLI and are set to something. Only names are returned, never values.
func ProviderKeysSet(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && manifest.IsProviderKeyVar(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// Authorize is the gate in front of every real run. It refuses a manifest with any issue (an
// unpinned identity, a candidate task, a budget short of the plan included), an approval that is
// not exactly the plan's run count, and an environment that sets a provider API key.
func Authorize(m *manifest.Manifest, approval int, environ []string) error {
	var errs []string
	for _, i := range m.Validate() {
		errs = append(errs, "manifest: "+i.String())
	}
	if n := m.PlannedRuns(); approval != n {
		errs = append(errs, fmt.Sprintf("a real run needs %s %d, the plan's run count, given by the owner's approval of that budget (got %d)",
			ApprovalFlag, n, approval))
	}
	if keys := ProviderKeysSet(environ); len(keys) > 0 {
		errs = append(errs, fmt.Sprintf("provider API key variables are set: %s; unset them, the benchmark runs CLIs on their subscription plans and never on API access",
			strings.Join(keys, ", ")))
	}
	if len(errs) > 0 {
		return errors.New("refusing to run:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// scrub is environ without any variable that would hand a CLI an API key or a pay-per-token
// route, as a second layer behind Authorize. The subscription's own login
// (CLAUDE_CODE_OAUTH_TOKEN) stays: a headless CLI needs it.
func scrub(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if manifest.IsProviderKeyVar(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
