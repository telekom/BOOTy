package plunderclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/telekom/BOOTy/pkg/plunderclient/types"
)

// GetConfigForAddress will retrieve the configuration for a server (mac address).
func GetConfigForAddress(mac string) (*types.BootyConfig, error) {
	// Attempt to find the Server URL
	url := os.Getenv("BOOTYURL")
	if url == "" {
		return nil, fmt.Errorf("the flag BOOTYURL is empty")
	}
	slog.Info("Connecting to provisioning server", "url", url) //nolint:gosec // url is from trusted env var, not user input

	// Address format

	// http:// address / booty / <mac> .bty

	// url = http://address/booty
	configURL := fmt.Sprintf("%s/%s.bty", url, mac)
	plunderClient := http.Client{
		Timeout: time.Second * 5, // Maximum of 5 secs
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, configURL, http.NoBody) //nolint:gosec // URL is constructed from trusted env var
	if err != nil {
		return nil, fmt.Errorf("creating config request: %w", err)
	}

	req.Header.Set("User-Agent", "BOOTy-client")

	res, err := plunderClient.Do(req) //nolint:gosec // URL is constructed from trusted env var
	if err != nil {
		return nil, fmt.Errorf("executing config request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode > 300 {
		// Customize response for the 404 to make debugging simpler
		if res.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%s not found", configURL)
		}
		return nil, fmt.Errorf("%s", res.Status)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading config response: %w", err)
	}

	var config types.BootyConfig

	err = json.Unmarshal(body, &config)
	if err != nil {
		slog.Error("Error reading config", "url", configURL) //nolint:gosec // configURL is from trusted env var, not user input
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &config, nil
}
