package httpapi

import (
	"time"

	"github.com/MontFerret/playground/internal/config"
	"github.com/MontFerret/playground/internal/playground"
)

func httpLimits(cfg config.Config) playground.HTTPLimits {
	return playground.HTTPLimits{
		MaxHeaderBytes:      cfg.MaxHeaderBytes,
		ReadHeaderTimeoutMs: float64(cfg.ReadHeaderTimeout) / float64(time.Millisecond),
		ReadTimeoutMs:       float64(cfg.ReadTimeout) / float64(time.Millisecond),
		WriteTimeoutMs:      float64(cfg.WriteTimeout) / float64(time.Millisecond),
		IdleTimeoutMs:       float64(cfg.IdleTimeout) / float64(time.Millisecond),
	}
}
