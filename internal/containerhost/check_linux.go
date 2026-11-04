package containerhost

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/MontFerret/playground/internal/config"
)

func checkAPI(ctx context.Context) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return errors.New("invalid_health_configuration")
	}
	_, port, _ := net.SplitHostPort(cfg.ListenAddr)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/v1/environment", nil)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("api_unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("api_unavailable")
	}

	return nil
}
