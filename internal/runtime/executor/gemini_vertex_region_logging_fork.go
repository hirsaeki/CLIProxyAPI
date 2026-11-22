package executor

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	log "github.com/sirupsen/logrus"
)

// vertexRegionTraceFork only records bounded route codes and decisions, never
// URLs, credentials, request bodies, response text, or transport error text.
type vertexRegionTraceFork struct {
	logger     *log.Entry
	ctx        context.Context
	model      string
	attempts   int
	active     bool
	lastRegion string
	primary    string
}

func newVertexRegionTraceFork(ctx context.Context, model, primary string) *vertexRegionTraceFork {
	return &vertexRegionTraceFork{ctx: ctx, model: model, primary: primary}
}

func (t *vertexRegionTraceFork) entry() *log.Entry {
	if t.logger != nil {
		return t.logger
	}
	// Do not print an arbitrary client-supplied model alias or location.
	model, primary := t.model, t.primary
	if len(model) > 128 || !strings.HasPrefix(model, "gemini-") || strings.IndexFunc(model, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.')
	}) >= 0 {
		model = "unrecognized"
	}
	if !vertexRegionLocationCodeFork(primary) {
		primary = "unrecognized"
	}
	t.logger = log.WithField("primary_region", primary)
	if model != "unrecognized" {
		t.logger = t.logger.WithField("model", model)
	}
	// HTTP middleware generates canonical UUIDv7 IDs. Embedded callers may
	// put other values in the context; do not print those unchecked.
	if raw := logging.GetRequestID(t.ctx); len(raw) == 36 {
		if id, err := uuid.Parse(raw); err == nil && id.Version() == 7 && id.String() == raw {
			t.logger = t.logger.WithField("request_id", raw)
		}
	}
	return t.logger
}

func (t *vertexRegionTraceFork) attempt(region string, phase, status int, result string) {
	if t == nil {
		return
	}
	t.attempts++
	t.lastRegion = region
	if !t.active && phase == 1 && region == t.primary && status >= 200 && status < 300 {
		return
	}
	t.active = true
	t.entry().WithFields(log.Fields{
		"region": region, "phase": phase, "attempt": t.attempts, "status": status, "result": result,
	}).Info("vertex regional HTTP attempt")
}

func (t *vertexRegionTraceFork) skip(region string, phase int, reason string) {
	if t == nil {
		return
	}
	t.active = true
	t.entry().WithFields(log.Fields{
		"region": region, "phase": phase, "reason": reason,
	}).Info("vertex regional candidate skipped")
}

func (t *vertexRegionTraceFork) finish(reason string, status int) {
	if t == nil || !t.active {
		return
	}
	entry := t.entry().WithFields(log.Fields{
		"reason": reason, "attempts": t.attempts, "status": status,
	})
	if t.lastRegion != "" {
		entry = entry.WithField("last_region", t.lastRegion)
	}
	if status >= 200 && status < 300 {
		entry.Info("vertex regional routing finished")
	} else {
		entry.Warn("vertex regional routing finished")
	}
}
