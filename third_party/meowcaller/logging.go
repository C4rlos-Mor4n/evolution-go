package meowcaller

import (
	"github.com/purpshell/meowcaller/diag"
	"github.com/rs/zerolog"
)

// Option configures optional, non-behavioral aspects of the call/media types —
// currently the diagnostic logger. The zero configuration logs nothing.
type Option func(*config)

type config struct {
	log       zerolog.Logger
	diag      *diag.Recorder
	milestone MilestoneFunc
}

// MilestoneFunc receives call setup/media milestones (KRIONIX PATCH). attrs only carry
// event names, booleans, counts, Meta relay names and error text: never JIDs, keys, IPs,
// SDP or media. It is called synchronously from signaling/media goroutines: keep it cheap.
type MilestoneFunc func(callID, name string, attrs map[string]string)

// WithMilestones registers a sanitized milestone callback (KRIONIX PATCH).
func WithMilestones(fn MilestoneFunc) Option {
	return func(c *config) { c.milestone = fn }
}

func resolveConfig(opts []Option) config {
	c := config{log: zerolog.Nop()}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// WithLogger sets the zerolog logger for debug/trace diagnostics. The library never
// configures logging itself; without this option the types are silent at zero cost.
// Pass the logger from a context, e.g. WithLogger(*zerolog.Ctx(ctx)).
func WithLogger(l zerolog.Logger) Option {
	return func(c *config) { c.log = l }
}

// WithDiagnostics attaches a developer-only *diag.Recorder that dumps exact,
// per-category call diagnostics (including raw secrets and media) to JSONL files.
// This is an opt-in maintainer carve-out from the library's sanitized logging and
// must never be enabled in production. Without it the recorder is nil and every
// diag emit is a no-op at zero cost.
func WithDiagnostics(rec *diag.Recorder) Option {
	return func(c *config) { c.diag = rec }
}
