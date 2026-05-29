package gemini

import (
	"testing"
	"time"
)

func TestNodePool_SelectAndBanFlow(t *testing.T) {
	t.Parallel()

	pool := NewNodePool([]string{"https://a", "https://b"}, PoolConfig{
		ExploreRatio:  0.5,
		FailThreshold: 2,
		BanBase:       50 * time.Millisecond,
		BanMax:        200 * time.Millisecond,
	})
	if pool == nil {
		t.Fatal("expected pool")
	}

	now := time.Now()
	primary, _ := pool.Select(now)
	if primary == "" {
		t.Fatal("expected non-empty primary")
	}
	pool.RecordFailure(primary, now)
	pool.RecordFailure(primary, now.Add(1*time.Millisecond))

	nextPrimary, _ := pool.Select(now.Add(2 * time.Millisecond))
	if nextPrimary == "" {
		t.Fatal("expected non-empty primary after ban")
	}
	if nextPrimary == primary {
		t.Fatalf("expected a different primary after banning %s", primary)
	}

	// After ban window, node becomes selectable again.
	afterBan := now.Add(300 * time.Millisecond)
	var seenRecovered bool
	for i := 0; i < 6; i++ {
		p, q := pool.Select(afterBan.Add(time.Duration(i) * time.Millisecond))
		if p == primary || q == primary {
			seenRecovered = true
			break
		}
	}
	if !seenRecovered {
		t.Fatalf("expected banned node %s to recover after ban window", primary)
	}
}

func TestNodePool_DedupAndNormalizeBases(t *testing.T) {
	t.Parallel()

	pool := NewNodePool([]string{" https://a/ ", "https://a", "", "https://b/"}, PoolConfig{})
	if pool == nil {
		t.Fatal("expected pool")
	}
	if len(pool.nodes) != 2 {
		t.Fatalf("expected deduped 2 nodes, got %d", len(pool.nodes))
	}
}
