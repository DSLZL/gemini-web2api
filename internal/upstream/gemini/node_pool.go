package gemini

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultExploreRatio  = 0.08
	defaultFailThreshold = 2
	defaultBanBase       = 2 * time.Minute
	defaultBanMax        = 10 * time.Minute
)

// PoolConfig controls node exploration and circuit-breaker behavior.
type PoolConfig struct {
	ExploreRatio  float64
	FailThreshold int
	BanBase       time.Duration
	BanMax        time.Duration
}

type nodeState struct {
	base             string
	consecutiveFails int
	successes        int64
	failures         int64
	lastSuccess      time.Time
	banUntil         time.Time
	everSucceeded    bool
}

// NodePool keeps node health state for exploit+explore routing.
type NodePool struct {
	mu           sync.Mutex
	cfg          PoolConfig
	nodes        []*nodeState
	requestCount int64
	probeCursor  int
}

func NewNodePool(bases []string, cfg PoolConfig) *NodePool {
	normalized := normalizeBases(bases)
	if len(normalized) == 0 {
		return nil
	}
	nodes := make([]*nodeState, 0, len(normalized))
	for _, base := range normalized {
		nodes = append(nodes, &nodeState{base: base})
	}
	return &NodePool{
		cfg:   normalizePoolConfig(cfg),
		nodes: nodes,
	}
}

func (p *NodePool) Select(now time.Time) (primary string, probe string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.nodes) == 0 {
		return "", ""
	}

	p.requestCount++
	candidates := p.availableNodesLocked(now)
	if len(candidates) == 0 {
		if recovered := p.releaseEarliestBannedLocked(); recovered != nil {
			candidates = append(candidates, recovered)
		}
	}
	if len(candidates) == 0 {
		return p.nodes[0].base, ""
	}

	mainNode := pickPrimaryNode(candidates)
	if !p.shouldExploreLocked() || len(candidates) <= 1 {
		return mainNode.base, ""
	}

	probeNode := p.pickProbeNodeLocked(candidates, mainNode)
	if probeNode == nil {
		return mainNode.base, ""
	}
	return mainNode.base, probeNode.base
}

func (p *NodePool) RecordSuccess(base string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	node := p.findNodeLocked(base)
	if node == nil {
		return
	}
	node.successes++
	node.everSucceeded = true
	node.consecutiveFails = 0
	node.lastSuccess = now
	node.banUntil = time.Time{}
}

func (p *NodePool) RecordFailure(base string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	node := p.findNodeLocked(base)
	if node == nil {
		return
	}
	node.failures++
	node.consecutiveFails++
	if node.consecutiveFails < p.cfg.FailThreshold {
		return
	}
	step := node.consecutiveFails - p.cfg.FailThreshold
	if step < 0 {
		step = 0
	}
	if step > 16 {
		step = 16
	}
	backoff := p.cfg.BanBase * time.Duration(1<<step)
	if backoff > p.cfg.BanMax {
		backoff = p.cfg.BanMax
	}
	node.banUntil = now.Add(backoff)
}

func normalizePoolConfig(cfg PoolConfig) PoolConfig {
	if cfg.ExploreRatio <= 0 {
		cfg.ExploreRatio = defaultExploreRatio
	}
	if cfg.ExploreRatio > 1 {
		cfg.ExploreRatio = 1
	}
	if cfg.FailThreshold <= 0 {
		cfg.FailThreshold = defaultFailThreshold
	}
	if cfg.BanBase <= 0 {
		cfg.BanBase = defaultBanBase
	}
	if cfg.BanMax <= 0 {
		cfg.BanMax = defaultBanMax
	}
	if cfg.BanMax < cfg.BanBase {
		cfg.BanMax = cfg.BanBase
	}
	return cfg
}

func normalizeBases(bases []string) []string {
	seen := make(map[string]struct{}, len(bases))
	out := make([]string, 0, len(bases))
	for _, raw := range bases {
		base := strings.TrimRight(strings.TrimSpace(raw), "/")
		if base == "" {
			continue
		}
		if _, ok := seen[base]; ok {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

func (p *NodePool) availableNodesLocked(now time.Time) []*nodeState {
	out := make([]*nodeState, 0, len(p.nodes))
	for _, node := range p.nodes {
		if now.Before(node.banUntil) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func (p *NodePool) releaseEarliestBannedLocked() *nodeState {
	var candidate *nodeState
	for _, node := range p.nodes {
		if node.banUntil.IsZero() {
			continue
		}
		if candidate == nil || node.banUntil.Before(candidate.banUntil) {
			candidate = node
		}
	}
	if candidate != nil {
		candidate.banUntil = time.Time{}
	}
	return candidate
}

func pickPrimaryNode(nodes []*nodeState) *nodeState {
	if len(nodes) == 1 {
		return nodes[0]
	}
	ordered := append([]*nodeState(nil), nodes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.everSucceeded != b.everSucceeded {
			return a.everSucceeded
		}
		if a.consecutiveFails != b.consecutiveFails {
			return a.consecutiveFails < b.consecutiveFails
		}
		if !a.lastSuccess.Equal(b.lastSuccess) {
			return a.lastSuccess.After(b.lastSuccess)
		}
		if a.failures != b.failures {
			return a.failures < b.failures
		}
		return a.base < b.base
	})
	return ordered[0]
}

func (p *NodePool) shouldExploreLocked() bool {
	if p.cfg.ExploreRatio >= 1 {
		return true
	}
	if p.cfg.ExploreRatio <= 0 {
		return false
	}
	interval := int64(math.Round(1 / p.cfg.ExploreRatio))
	if interval <= 1 {
		return true
	}
	return p.requestCount%interval == 0
}

func (p *NodePool) pickProbeNodeLocked(nodes []*nodeState, primary *nodeState) *nodeState {
	candidates := make([]*nodeState, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.base == primary.base {
			continue
		}
		candidates = append(candidates, node)
	}
	if len(candidates) == 0 {
		return nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.everSucceeded != b.everSucceeded {
			return !a.everSucceeded
		}
		if !a.lastSuccess.Equal(b.lastSuccess) {
			return a.lastSuccess.Before(b.lastSuccess)
		}
		if a.failures != b.failures {
			return a.failures > b.failures
		}
		return a.base < b.base
	})

	idx := p.probeCursor % len(candidates)
	p.probeCursor++
	return candidates[idx]
}

func (p *NodePool) findNodeLocked(base string) *nodeState {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, node := range p.nodes {
		if node.base == normalized {
			return node
		}
	}
	return nil
}

