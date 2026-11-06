package mcpserver

import (
	"errors"
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

func TestLatestReplacedEveryTimeEndsInTheError(t *testing.T) {
	attempts, pauses := 0, 0
	s := &stores{
		latest: func(string) (string, error) {
			attempts++
			return "", fmt.Errorf("latest: %w", store.ErrReplaced)
		},
		pause: func(time.Duration) { pauses++ },
	}
	if _, err := s.readLatest(); !errors.Is(err, store.ErrReplaced) || attempts != latestAttempts || pauses != latestAttempts-1 {
		t.Fatalf("readLatest = %v after %d attempts and %d pauses, want ErrReplaced after %d and %d", err, attempts, pauses, latestAttempts, latestAttempts-1)
	}
}
