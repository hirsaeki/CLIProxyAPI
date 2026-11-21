package cliproxy

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	log "github.com/sirupsen/logrus"
)

func (s *Service) startVertexLocationInventoryFork(ctx context.Context) {
	s.vertexLocations.Start(ctx, func() string {
		if cfg := s.currentConfig(); cfg != nil {
			return cfg.ProxyURL
		}
		return ""
	}, logVertexRefreshFork)
}

func logVertexRefreshFork(status vertexlocations.RefreshStatus) {
	fields := log.Fields{"state": status.State, "model_count": status.ModelCount}
	if !status.VerifiedAt.IsZero() {
		fields["verified_at"] = status.VerifiedAt.UTC()
		fields["fresh_until"] = status.FreshUntil.UTC()
	}
	entry := log.WithFields(fields)
	if status.Err != nil {
		entry.WithError(status.Err).Warn("Vertex location inventory refresh failed")
	} else {
		entry.Info("Vertex location inventory verified")
	}
}
