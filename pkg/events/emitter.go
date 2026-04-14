package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Emitter serializes Event values to JSON Lines and writes them to a
// destination (stdout or a rotating file). It is safe for concurrent use.
type Emitter struct {
	mu       sync.Mutex
	writer   io.Writer
	closer   io.Closer
	rotator  *fileRotator
	filter   map[Type]struct{}
	filterOn bool
}

// NewEmitter builds an emitter from the exporter configuration. When the
// configured output is "stdout" it writes to os.Stdout, otherwise it opens
// (or constructs, for daily rotation) the target file.
func NewEmitter(cfg *config.Config) (*Emitter, error) {
	e := &Emitter{}

	if len(cfg.EventsTypes) > 0 {
		e.filterOn = true
		e.filter = make(map[Type]struct{}, len(cfg.EventsTypes))
		known := make(map[Type]struct{}, len(AllTypes()))
		for _, t := range AllTypes() {
			known[t] = struct{}{}
		}
		var unknown []string
		for _, t := range cfg.EventsTypes {
			name := Type(strings.ToLower(strings.TrimSpace(t)))
			if _, ok := known[name]; !ok {
				unknown = append(unknown, string(name))
				continue
			}
			e.filter[name] = struct{}{}
		}
		if len(unknown) > 0 {
			logger.SafeWarn("events", "Unknown OPENLDAP_EVENTS_TYPES entries ignored", map[string]interface{}{
				"unknown": unknown,
				"valid":   AllTypes(),
			})
		}
		if len(e.filter) == 0 {
			// No valid filter entries → behave as "all".
			e.filterOn = false
		}
	}

	output := strings.TrimSpace(cfg.EventsOutput)
	if output == "" || strings.EqualFold(output, "stdout") {
		e.writer = os.Stdout
		return e, nil
	}

	switch cfg.EventsRotation {
	case config.EventsRotationDaily:
		rot, err := newFileRotator(output, true)
		if err != nil {
			return nil, err
		}
		e.rotator = rot
		e.writer = rot
		e.closer = rot
	default:
		rot, err := newFileRotator(output, false)
		if err != nil {
			return nil, err
		}
		e.rotator = rot
		e.writer = rot
		e.closer = rot
	}

	return e, nil
}

// Emit writes a single event as one JSON line. Events filtered out by the
// configured type allow-list are silently dropped.
func (e *Emitter) Emit(ev Event) {
	if e == nil {
		return
	}
	if e.filterOn {
		if _, ok := e.filter[ev.Event]; !ok {
			return
		}
	}

	payload, err := json.Marshal(ev)
	if err != nil {
		logger.SafeError("events", "Failed to marshal event", err, map[string]interface{}{
			"event": string(ev.Event),
		})
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.writer.Write(append(payload, '\n')); err != nil {
		logger.SafeError("events", "Failed to write event", err, map[string]interface{}{
			"event": string(ev.Event),
		})
	}
}

// Close releases any underlying file handle. Stdout is never closed.
func (e *Emitter) Close() error {
	if e == nil || e.closer == nil {
		return nil
	}
	return e.closer.Close()
}

// fileRotator writes events to a file, optionally rolling to a new file every
// UTC day. It is intentionally minimal — no size limits, no compression — the
// goal is "cheap, dependency-free, survives restarts"; heavy rotation needs
// are better delegated to logrotate or a log shipper.
type fileRotator struct {
	mu          sync.Mutex
	template    string
	daily       bool
	currentPath string
	currentDay  string
	file        *os.File
	// clock is injected in tests to simulate day boundaries.
	clock func() time.Time
}

func newFileRotator(output string, daily bool) (*fileRotator, error) {
	r := &fileRotator{
		template: output,
		daily:    daily,
		clock:    func() time.Time { return time.Now().UTC() },
	}
	if err := r.ensureDir(); err != nil {
		return nil, err
	}
	if err := r.rollIfNeeded(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *fileRotator) ensureDir() error {
	dir := filepath.Dir(r.resolvePathFor(r.clock()))
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wrapPermError("create output directory", dir, err)
	}
	return nil
}

// wrapPermError decorates a filesystem error with the current process UID/GID
// and a concrete remediation hint when the failure is a permission denied on
// a host-mounted path. The runtime UID is frequently mismatched between the
// container (distroless/static uses 65532) and the host owner of the bind
// mount, which is by far the most common failure mode when enabling the
// events stream on a filesystem path.
func wrapPermError(action, path string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("events: %s %s: %w", action, path, err)
	}
	return fmt.Errorf(
		"events: %s %s: %w — process runs as uid=%d gid=%d; ensure the host path is writable by that uid "+
			"(e.g. `install -d -o %d -g %d %s` on the host, or set `user: \"%d:%d\"` on the exporter service "+
			"to match the bind-mount owner)",
		action, path, err, os.Getuid(), os.Getgid(),
		os.Getuid(), os.Getgid(), filepath.Dir(path),
		os.Getuid(), os.Getgid(),
	)
}

// resolvePathFor substitutes the {date} token in the configured template. If
// the token is absent and daily rotation is requested we append a
// ".YYYY-MM-DD" suffix so distinct days still land in distinct files.
func (r *fileRotator) resolvePathFor(now time.Time) string {
	day := now.Format("2006-01-02")
	if strings.Contains(r.template, "{date}") {
		return strings.ReplaceAll(r.template, "{date}", day)
	}
	if r.daily {
		return r.template + "." + day
	}
	return r.template
}

func (r *fileRotator) rollIfNeeded() error {
	now := r.clock()
	day := now.Format("2006-01-02")
	path := r.resolvePathFor(now)

	if r.file != nil && path == r.currentPath && (!r.daily || day == r.currentDay) {
		return nil
	}

	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}

	// 0644: owner rw, everyone read. Deliberately world-readable so log
	// shippers (Promtail, Vector, Fluent Bit, …) or ad-hoc `tail` sessions
	// running under a different UID than the exporter process can consume
	// the stream without having to share a Unix group with it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return wrapPermError("open", path, err)
	}
	r.file = f
	r.currentPath = path
	r.currentDay = day
	return nil
}

// Write implements io.Writer. It rolls the underlying file to the current day
// on demand, so a long-running process naturally starts a new file at UTC
// midnight without external intervention.
func (r *fileRotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.rollIfNeeded(); err != nil {
		return 0, err
	}
	return r.file.Write(p)
}

// Close releases the underlying file handle.
func (r *fileRotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
