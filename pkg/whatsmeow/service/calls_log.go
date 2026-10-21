package whatsmeow_service

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// meowcallerSensitiveKeys are dropped from every meowcaller log line: identifiers of the
// people on the call (JIDs/LIDs/phones) and network addresses. meowcaller's logger never
// carries key material at these levels (secrets only go to its separate diag recorder,
// which is not enabled), but identifiers do appear at info level.
var meowcallerSensitiveKeys = map[string]bool{
	"peer": true, "peer_lid": true, "from": true, "to": true, "jid": true, "lid": true,
	"creator": true, "call_creator": true, "phone": true, "target": true,
	"addr": true, "address": true, "ipv4": true, "ip": true, "relay_addr": true,
	"self": true, "self_lid": true, "device": true, "participant": true,
}

// sanitizingWriter re-encodes each zerolog JSON line without the sensitive keys and
// prefixes it so it is easy to grep. Lines that are not JSON are dropped (never echoed).
type sanitizingWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *sanitizingWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimSpace(p), []byte("\n")) {
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			continue
		}
		for k := range fields {
			if meowcallerSensitiveKeys[strings.ToLower(k)] {
				delete(fields, k)
			}
		}
		clean, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		w.mu.Lock()
		_, _ = w.out.Write(append(append([]byte("[meowcaller] "), clean...), '\n'))
		w.mu.Unlock()
	}
	return len(p), nil
}

// meowcallerLogger returns the sanitized logger handed to meowcaller.
// CALLS_LOG_LEVEL: error | warn (default) | info. debug/trace are refused on purpose:
// they are not needed to diagnose call setup and may carry raw stanzas.
func meowcallerLogger(level string) zerolog.Logger {
	lvl := zerolog.WarnLevel
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error":
		lvl = zerolog.ErrorLevel
	case "info":
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(&sanitizingWriter{out: os.Stdout}).Level(lvl).With().Timestamp().Logger()
}
