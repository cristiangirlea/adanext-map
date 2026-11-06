package mcpserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/store"
)

func TestLatestReplacedWhileOpenedIsRetried(t *testing.T) {
	attempts := 0
	s := &stores{
		latest: func(string) (string, error) {
			attempts++
			if attempts == 1 {
				return "", fmt.Errorf("latest: %w", store.ErrReplaced)
			}
			return commitA, nil
		},
		pause: func(time.Duration) {},
	}
	if commit, err := s.readLatest(); err != nil || commit != commitA || attempts != 2 {
		t.Fatalf("readLatest = %q, %v after %d attempts, want %q, nil after 2", commit, err, attempts, commitA)
	}
}
