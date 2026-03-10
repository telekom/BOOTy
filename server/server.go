package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/telekom/BOOTy/pkg/plunderclient/types"
	"github.com/telekom/BOOTy/pkg/utils"
)

// Server holds the state for the BOOTy provisioning server.
type Server struct {
	configData []byte
}

// WriteCounter counts the number of bytes written to it and reports progress.
type WriteCounter struct {
	Total uint64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total += uint64(n)
	wc.PrintProgress()
	return n, nil
}

// PrintProgress displays write progress.
func (wc *WriteCounter) PrintProgress() {
	fmt.Printf("\r%s", strings.Repeat(" ", 35))
	fmt.Printf("\rDownloading... %s complete", humanize.Bytes(wc.Total))
	fmt.Println("")
}

func (s *Server) imageHandler(w http.ResponseWriter, r *http.Request) {
	imageName := fmt.Sprintf("%s.img", r.RemoteAddr)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		slog.Error("Error parsing multipart form", "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("BootyImage")
	if err != nil {
		slog.Error("Error getting form file", "error", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	out, err := os.OpenFile(imageName, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // image files need standard read permissions
	if err != nil {
		slog.Error("Error opening file", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = out.Close() }()

	// Create our progress reporter and pass it to be used alongside our writer
	counter := &WriteCounter{}
	if _, err = io.Copy(out, io.TeeReader(file, counter)); err != nil {
		slog.Error("Error copying image", "error", err)
	}

	slog.Info("Image received", "image", imageName) //nolint:gosec // imageName is derived from RemoteAddr, not arbitrary user input
	w.WriteHeader(http.StatusOK)
}

func (s *Server) configHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := w.Write(s.configData); err != nil { //nolint:gosec // configData is server-controlled JSON, not user input
		slog.Error("Error writing config response", "error", err)
	}
}

// setupServer configures HTTP handlers and returns the server mux.
// Extracted from main() for testability.
func (s *Server) setupServer(rawMac string, config *types.BootyConfig) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	if rawMac == "" {
		slog.Warn("No Mac address passed for BOOTy configuration")
	} else {
		dashmac := utils.DashMac(rawMac)
		mux.HandleFunc(fmt.Sprintf("/booty/%s.bty", dashmac), s.configHandler)
		slog.Info("Handler generated", "config", dashmac+".bty")
		var err error
		s.configData, err = json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("marshaling config: %w", err)
		}
	}

	switch config.Action {
	case types.ReadImage, types.WriteImage:
	default:
		return nil, fmt.Errorf("unknown action: %s", config.Action)
	}

	fs := http.FileServer(http.Dir("./images"))
	mux.HandleFunc("/image", s.imageHandler)
	mux.Handle("/images/", http.StripPrefix("/images/", fs))

	return mux, nil
}

func main() {
	mac, config, err := parseFlags(os.Args[1:])
	if err != nil {
		slog.Error("Fatal", "error", err)
		os.Exit(1)
	}
	srv := &Server{}
	mux, err := srv.setupServer(mac, &config)
	if err != nil {
		slog.Error("Fatal", "error", err)
		os.Exit(1)
	}
	slog.Info("Listening on :3000...")
	server := &http.Server{
		Addr:              ":3000",
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (string, types.BootyConfig, error) {
	fs := flag.NewFlagSet("booty-server", flag.ContinueOnError)
	rawAddress := fs.String("mac", "", "The mac address of a server")

	var config types.BootyConfig
	fs.StringVar(&config.Action, "action", "", "The action that is being performed [readImage/writeImage]")
	fs.BoolVar(&config.DryRun, "dryRun", false, "Only demonstrate the output from the actions")
	fs.BoolVar(&config.DropToShell, "shell", false, "Start a shell")
	fs.BoolVar(&config.WipeDevice, "wipe", false, "Wipe the device [OnError]")

	fs.StringVar(&config.LVMRootName, "lvmRoot", "/dev/ubuntu-vg/root", "The path to the root Linux volume")
	fs.IntVar(&config.GrowPartition, "growPartition", 1, "The partition on the destinationDevice that should be grown")

	fs.StringVar(&config.SourceImage, "sourceImage", "", "The source for the image, typically a URL")
	fs.StringVar(&config.SourceDevice, "sourceDevice", "", "The device that will be the source of the image [/dev/sda]")

	fs.StringVar(&config.DestinationAddress, "destinationAddress", "", "The destination that the image will be written to [url]")
	fs.StringVar(&config.DestinationDevice, "destinationDevice", "", "The destination device that the image will be written to [/dev/sda]")

	fs.StringVar(&config.Address, "address", "", "The network address to set on the provisioned OS [address/subnet]")
	fs.StringVar(&config.Gateway, "gateway", "", "The gateway address to be set on the provisioned OS")

	if err := fs.Parse(args); err != nil {
		return "", types.BootyConfig{}, fmt.Errorf("parsing flags: %w", err)
	}

	return *rawAddress, config, nil
}
