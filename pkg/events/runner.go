package events

import (
	"sync"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Runner drives the events loop: it owns a cursor and an emitter, ticks at the
// configured interval, and calls the collector to translate new accesslog
// entries into JSON events. It is started once at process boot and stopped
// during graceful shutdown.
type Runner struct {
	cfg       *config.Config
	collector *collector
	emitter   *Emitter
	cursor    *cursorState

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewRunner wires the emitter, cursor and collector for a given LDAP client
// and server name. Callers should invoke Start exactly once; Stop is safe to
// call multiple times.
func NewRunner(cfg *config.Config, client accesslogSearcher) (*Runner, error) {
	emitter, err := NewEmitter(cfg)
	if err != nil {
		return nil, err
	}
	return &Runner{
		cfg:       cfg,
		collector: newCollector(client, cfg.ServerName),
		emitter:   emitter,
		cursor:    &cursorState{},
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}, nil
}

// Start launches the ticker goroutine. It returns immediately.
func (r *Runner) Start() {
	go r.loop()
}

func (r *Runner) loop() {
	defer close(r.done)

	interval := r.cfg.EventsInterval
	if interval <= 0 {
		interval = config.DefaultEventsInterval
	}

	// First tick: short delay so we observe the same baseline semantics as the
	// metric collector (establish cursors without back-filling history) before
	// the user sees the first real events.
	initial := time.NewTimer(time.Second)
	select {
	case <-initial.C:
	case <-r.stop:
		initial.Stop()
		return
	}
	r.tick()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.SafeInfo("events", "Events runner started", map[string]interface{}{
		"interval": interval.String(),
		"output":   r.cfg.EventsOutput,
		"rotation": string(r.cfg.EventsRotation),
		"types":    r.cfg.EventsTypes,
	})

	for {
		select {
		case <-ticker.C:
			r.tick()
		case <-r.stop:
			return
		}
	}
}

func (r *Runner) tick() {
	count, err := r.collector.scan(r.cursor, r.emitter.Emit)
	if err != nil {
		logger.SafeDebug("events", "Events scan failed", map[string]interface{}{
			"emitted": count,
			"error":   err.Error(),
		})
		return
	}
	if count > 0 {
		logger.SafeDebug("events", "Events emitted", map[string]interface{}{
			"count": count,
		})
	}
}

// Stop signals the loop to exit, waits for it to return, and releases the
// emitter's underlying writer (file handle, if any).
func (r *Runner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stop)
	})
	<-r.done
	if err := r.emitter.Close(); err != nil {
		logger.SafeError("events", "Failed to close events emitter", err)
	}
}
