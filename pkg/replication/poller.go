// Package replication implements the single-point replication peer poller: from
// one node it connects to each replication peer, reads their contextCSN, and
// computes true cross-node propagation lag centrally — no agent required on the
// peers. It runs on its own ticker with a dedicated pool + circuit breaker per
// peer, mirroring the isolation of the events stream.
package replication

import (
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/csn"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/metrics"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/pool"
)

// csnKey identifies one contextCSN component: a naming context plus the master
// RID (server_id) that owns that component.
type csnKey struct {
	baseDN   string
	serverID string
}

// sourceResult is one node's contribution to a poll round.
type sourceResult struct {
	peer string // peer URI, empty for the local node
	csns map[csnKey]float64
	up   bool
}

// Poller polls replication peers' contextCSN and exposes per-peer reachability,
// CSN timestamps and propagation lag.
type Poller struct {
	cfg     *config.Config
	local   *pool.PooledLDAPClient
	metrics *metrics.OpenLDAPMetrics

	peers map[string]*pool.PooledLDAPClient // peer URI -> dedicated client

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewPoller creates the peer poller. Peers are not resolved here: discovery
// runs on every tick via reconcilePeers, so peers added or removed in cn=config
// (or in OPENLDAP_REPLICATION_PEERS after a restart) are picked up without
// restarting the poller. The poller is always returned when polling is enabled;
// it simply emits nothing until at least one peer is resolved.
func NewPoller(cfg *config.Config, local *pool.PooledLDAPClient, m *metrics.OpenLDAPMetrics) *Poller {
	logger.SafeInfo("replication", "Replication peer poller created", map[string]interface{}{
		"interval":      pollInterval(cfg).String(),
		"manual_peers":  len(cfg.ReplicationPeers),
		"auto_discover": local != nil,
	})

	return &Poller{
		cfg:     cfg,
		local:   local,
		metrics: m,
		peers:   make(map[string]*pool.PooledLDAPClient),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func pollInterval(cfg *config.Config) time.Duration {
	if cfg.ReplicationPollInterval <= 0 {
		return config.DefaultReplPollInterval
	}
	return cfg.ReplicationPollInterval
}

// Start launches the ticker goroutine. It returns immediately.
func (p *Poller) Start() {
	go p.loop()
}

func (p *Poller) loop() {
	defer close(p.done)

	// Short initial delay so the first poll does not race process start-up.
	initial := time.NewTimer(time.Second)
	select {
	case <-initial.C:
	case <-p.stop:
		initial.Stop()
		return
	}
	p.reconcilePeers()
	p.poll()

	ticker := time.NewTicker(pollInterval(p.cfg))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Re-discover before each poll so topology changes (new/removed
			// peers) are reflected without a restart. reconcile and poll run
			// sequentially in this single goroutine, and poll's per-peer
			// goroutines are joined before the next reconcile, so the peers
			// map is never mutated concurrently with a read.
			p.reconcilePeers()
			p.poll()
		case <-p.stop:
			return
		}
	}
}

// reconcilePeers re-runs discovery and brings the live set of per-peer clients
// in line with it: new peers get a dedicated client, peers that have genuinely
// disappeared are closed and their metric series pruned. A peer is only removed
// when discovery was authoritative this round (reliable), so a transient
// cn=config read failure never tears down known peers.
func (p *Poller) reconcilePeers() {
	discovered, reliable := discoverPeers(p.cfg, p.local)

	want := make(map[string]struct{}, len(discovered))
	for _, uri := range discovered {
		want[uri] = struct{}{}
		if _, ok := p.peers[uri]; !ok {
			p.peers[uri] = buildPeerClient(p.cfg, uri)
			logger.SafeInfo("replication", "Replication peer added", map[string]interface{}{"peer": uri})
		}
	}

	if !reliable {
		return
	}

	for uri, client := range p.peers {
		if _, ok := want[uri]; ok {
			continue
		}
		client.Close()
		delete(p.peers, uri)
		p.prunePeerMetrics(uri)
		logger.SafeInfo("replication", "Replication peer removed", map[string]interface{}{"peer": uri})
	}
}

// prunePeerMetrics drops every series carrying a removed peer's label so a
// vanished peer does not linger as stale gauges.
func (p *Poller) prunePeerMetrics(peer string) {
	labels := prometheus.Labels{"peer": peer}
	p.metrics.ReplicationPeerUp.DeletePartialMatch(labels)
	p.metrics.ReplicationPeerCSN.DeletePartialMatch(labels)
	p.metrics.ReplicationPeerLag.DeletePartialMatch(labels)
}

// poll runs one round: read the local and every peer's contextCSN concurrently,
// derive the per-(base_dn, server_id) reference (the most-advanced source), then
// publish per-peer reachability, CSN and lag.
func (p *Poller) poll() {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []sourceResult
	)

	read := func(peer string, client *pool.PooledLDAPClient) {
		defer wg.Done()
		csns, up := readCSN(client)
		mu.Lock()
		results = append(results, sourceResult{peer: peer, csns: csns, up: up})
		mu.Unlock()
	}

	wg.Add(1)
	go read("", p.local) // local node participates in the reference but emits no peer series
	for uri, client := range p.peers {
		wg.Add(1)
		go read(uri, client)
	}
	wg.Wait()

	// Reference per key = the highest CSN seen across local + all peers.
	ref := make(map[csnKey]float64)
	for _, r := range results {
		for k, ts := range r.csns {
			if ts > ref[k] {
				ref[k] = ts
			}
		}
	}

	for _, r := range results {
		if r.peer == "" {
			continue // local node has its own metrics elsewhere
		}

		up := 0.0
		if r.up {
			up = 1.0
		}
		p.metrics.ReplicationPeerUp.WithLabelValues(r.peer).Set(up)

		for k, ts := range r.csns {
			p.metrics.ReplicationPeerCSN.WithLabelValues(r.peer, k.baseDN, k.serverID).Set(ts)
			lag := ref[k] - ts
			if lag < 0 {
				lag = 0
			}
			p.metrics.ReplicationPeerLag.WithLabelValues(r.peer, k.baseDN, k.serverID).Set(lag)
		}
	}
}

// readCSN reads every data naming context's contextCSN from a single node and
// returns a map keyed by (base_dn, server_id). The bool reports reachability:
// false when the node could not be queried at all (connection/bind failure).
func readCSN(client *pool.PooledLDAPClient) (map[csnKey]float64, bool) {
	rootDSE, err := client.SearchRootDSE([]string{"namingContexts"})
	if err != nil || len(rootDSE.Entries) == 0 {
		return nil, false
	}

	csns := make(map[csnKey]float64)
	for _, attr := range rootDSE.Entries[0].Attributes {
		if attr.Name != "namingContexts" {
			continue
		}
		for _, suffix := range attr.Values {
			// Skip configuration/monitor trees; only data suffixes carry contextCSN.
			if strings.HasPrefix(strings.ToLower(suffix), "cn=") {
				continue
			}
			res, err := client.SearchContextCSN(suffix)
			if err != nil || len(res.Entries) == 0 {
				continue
			}
			for _, a := range res.Entries[0].Attributes {
				if a.Name != "contextCSN" {
					continue
				}
				for _, v := range a.Values {
					ts, sid, err := csn.Parse(v)
					if err != nil {
						continue
					}
					key := csnKey{baseDN: suffix, serverID: sid}
					if epoch := float64(ts.Unix()); epoch > csns[key] {
						csns[key] = epoch
					}
				}
			}
		}
	}

	return csns, true
}

// Stop signals the loop to exit, waits for it to return, and closes every
// peer's dedicated client. The local client is owned by the exporter and is not
// closed here.
func (p *Poller) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		close(p.stop)
	})
	<-p.done
	for _, client := range p.peers {
		client.Close()
	}
}
