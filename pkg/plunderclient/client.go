package plunderclient

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/telekom/BOOTy/pkg/plunderclient/types"
)

// GetConfigForAddress will retrieve the configuration for a server (mac address)
func GetConfigForAddress(mac string) (*types.BootyConfig, error) {
	// Attempt to find the Server URL
	url := os.Getenv("BOOTYURL")
	if url == "" {
		return nil, fmt.Errorf("the flag BOOTYURL is empty")
	}
	slog.Info("Connecting to provisioning server", "url", url)

	// Address format

	// http:// address / booty / <mac> .bty

	// url = http://address/booty
	configURL := fmt.Sprintf("%s/%s.bty", url, mac)
	plunderClient := http.Client{
		Timeout: time.Second * 5, // Maximum of 5 secs
	}

	req, err := http.NewRequest(http.MethodGet, configURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "BOOTy-client")

	res, err := plunderClient.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode > 300 {
		// Customize response for the 404 to make debugging simpler
		if res.StatusCode == 404 {
			return nil, fmt.Errorf("%s not found", configURL)
		}
		return nil, fmt.Errorf("%s", res.Status)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var config types.BootyConfig

	err = json.Unmarshal(body, &config)
	if err != nil {
		slog.Error("Error reading config", "url", configURL)
		return nil, err
	}

	return &config, nil
}
