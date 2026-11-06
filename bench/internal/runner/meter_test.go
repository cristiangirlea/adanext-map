package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

func TestWatchChecksFinalLine(t *testing.T) {
	if os.Getenv("ADAMAP_FINAL_LINE_HELPER") != "" {
		os.Stdout.WriteString(`{"type":"system","subtype":"init","apiKeySource":"none"}` + "\n")
		os.Stdout.WriteString(os.Getenv("ADAMAP_FINAL_LINE_HELPER"))
		if os.Getenv("ADAMAP_FINAL_LINE_DELAY") != "" {
			time.Sleep(20 * time.Second)
		}
		os.Exit(0)
	}
	for _, tc := range []struct {
		line string
		want error
	}{
		{`{"type":"rate_limit_event","rate_limit_info":{"isUsingOverage":true}}`, ErrOverage},
		{`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`, ErrPlanLimit},
		{`{"type":"result","result":"ok"}`, nil},
	} {
		env := append(os.Environ(), "ADAMAP_FINAL_LINE_HELPER="+tc.line)
		res := runWatched(context.Background(), []string{os.Args[0], "-test.run=^TestWatchChecksFinalLine$"}, "", env, "", 10*time.Second, subscriptionOnly(nil))
		if !errors.Is(res.refused, tc.want) {
			t.Errorf("final line: %v, want %v", res.refused, tc.want)
		}
	}
}

func TestReserveRejectsImplausibleReset(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, reset := range []int64{now.UnixMilli(), now.Add(8*24*time.Hour + time.Second).Unix(), 0} {
		if err := reserveReached(map[string]record.PlanWindow{"weekly": {Utilization: 0.9, ResetsAt: reset}}, 0.8, now); err != nil {
			t.Errorf("unknown reset %d blocks: %v", reset, err)
		}
	}
	if err := reserveReached(map[string]record.PlanWindow{"weekly": {Utilization: 0.9, ResetsAt: now.Add(8 * 24 * time.Hour).Unix()}}, 0.8, now); !errors.Is(err, ErrPlanReserve) {
		t.Fatalf("valid reset: %v", err)
	}
}

func TestMeterUsesReadingTimestampAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	base := time.Unix(1800000000, 0)
	stamp := func(minutes int) string {
		return base.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339Nano)
	}
	for _, raw := range []struct{ name, data string }{
		{"a.json", fmt.Sprintf(`{"run_id":"a","started_at":%q,"plan_meter":{"five_hour":{"utilization":0.9,"resets_at":1800003600,"observed_at":%q}}}`, stamp(0), stamp(5))},
		{"b.json", fmt.Sprintf(`{"run_id":"b","started_at":%q,"plan_meter":{"five_hour":{"utilization":0.1,"resets_at":1800003600,"observed_at":%q}}}`, stamp(6), base.Add(4*time.Minute).In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano))},
	} {
		if err := os.WriteFile(filepath.Join(dir, raw.name), []byte(raw.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	meter, err := lastMeter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meter["five_hour"].Utilization != 0.9 {
		t.Fatalf("stale reading wins: %+v", meter)
	}
}

func TestMeterPreservesEventTimestamp(t *testing.T) {
	newer := time.Unix(1800000300, 0).UTC().Format(time.RFC3339Nano)
	older := time.Unix(1800000240, 0).UTC().Format(time.RFC3339Nano)
	transcript := fmt.Sprintf(`{"type":"rate_limit_event","timestamp":%q,"rate_limit_info":{"rateLimitType":"five_hour","utilization":0.9}}`+"\n"+`{"type":"rate_limit_event","timestamp":%q,"rate_limit_info":{"rateLimitType":"five_hour","utilization":0.1}}`, newer, older)
	out := (Claude{}).Parse([]byte(transcript), manifest.Backend{})
	data, err := json.Marshal(out.Meter)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"observed_at":"`+newer+`"`) || out.Meter["five_hour"].Utilization != 0.9 {
		t.Fatalf("meter: %s", data)
	}
}

func TestWatchTimestampsMeterOnArrival(t *testing.T) {
	meter := map[string]record.PlanWindow{}
	watch := subscriptionOnly(meter)
	before := time.Now()
	if err := watch([]byte(`{"type":"rate_limit_event","rate_limit_info":{"rateLimitType":"five_hour","utilization":0.9}}`)); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, meter["five_hour"].ObservedAt)
	if err != nil || at.Before(before) || at.After(time.Now()) {
		t.Fatalf("arrival time: %v (%v)", at, err)
	}
	older := at.Add(-time.Minute).Format(time.RFC3339Nano)
	if err := watch([]byte(`{"type":"rate_limit_event","timestamp":"` + older + `","rate_limit_info":{"rateLimitType":"five_hour","utilization":0.1}}`)); err != nil {
		t.Fatal(err)
	}
	if meter["five_hour"].Utilization != 0.9 {
		t.Fatalf("stale event replaced arrival reading: %+v", meter)
	}
}

func TestMergeMeterPreservesNewestAcrossRuns(t *testing.T) {
	meter := map[string]record.PlanWindow{}
	base := time.Unix(1800000000, 0).UTC()
	for _, tc := range []struct {
		at  string
		use float64
	}{
		{base.Add(5 * time.Minute).Format(time.RFC3339Nano), 0.9},
		{base.Add(4 * time.Minute).In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano), 0.1},
		{"", 0.2},
	} {
		mergeMeter(meter, map[string]record.PlanWindow{"five_hour": {Utilization: tc.use, ObservedAt: tc.at}})
		if meter["five_hour"].Utilization != 0.9 {
			t.Fatalf("stale reading replaced latest: %+v", meter)
		}
	}
	mergeMeter(meter, map[string]record.PlanWindow{"five_hour": {Utilization: 0.3, ObservedAt: base.Add(6 * time.Minute).Format(time.RFC3339Nano)}})
	if meter["five_hour"].Utilization != 0.3 {
		t.Fatalf("new reading ignored: %+v", meter)
	}
}

func TestMeterLoadsLegacyRecord(t *testing.T) {
	dir := t.TempDir()
	started := time.Unix(1800000000, 0).UTC().Format(time.RFC3339Nano)
	data := fmt.Sprintf(`{"run_id":"old","started_at":%q,"plan_meter":{"five_hour":{"utilization":0.9,"resets_at":1800003600}}}`, started)
	if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	meter, err := lastMeter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meter["five_hour"].ObservedAt != started || meter["five_hour"].Utilization != 0.9 {
		t.Fatalf("legacy reading lost: %+v", meter)
	}
}

func TestFinalRejectedLineDoesNotReplaceTimeout(t *testing.T) {
	env := append(os.Environ(), `ADAMAP_FINAL_LINE_HELPER={"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`, "ADAMAP_FINAL_LINE_DELAY=1")
	res := runWatched(context.Background(), []string{os.Args[0], "-test.run=^TestWatchChecksFinalLine$"}, "", env, "", time.Second, subscriptionOnly(nil))
	if !res.timedOut || res.refused != nil {
		t.Fatalf("timeout replaced by final meter: %+v", res)
	}
}
