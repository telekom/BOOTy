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

	"github.com/dustin/go-humanize"
	"github.com/telekom/BOOTy/pkg/plunderclient/types"
	"github.com/telekom/BOOTy/pkg/utils"
)

// WriteCounter counts the number of bytes written to it. It implements to the io.Writer interface
// and we can pass this into io.TeeReader() which will report progress on each write cycle.
type WriteCounter struct {
	Total uint64
}

var data []byte

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total += uint64(n)
	wc.PrintProgress()
	return n, nil
}

// PrintProgress displays write progress.
func (wc WriteCounter) PrintProgress() {
	fmt.Printf("\r%s", strings.Repeat(" ", 35))
	fmt.Printf("\rDownloading... %s complete", humanize.Bytes(wc.Total))
	fmt.Println("")
}

func imageHandler(w http.ResponseWriter, r *http.Request) {

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
	defer file.Close()

	out, err := os.OpenFile(imageName, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("Error opening file", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	// Create our progress reporter and pass it to be used alongside our writer
	counter := &WriteCounter{}
	if _, err = io.Copy(out, io.TeeReader(file, counter)); err != nil {
		slog.Error("Error copying image", "error", err)
	}

	slog.Info("Image received", "image", imageName)
	w.WriteHeader(http.StatusOK)
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := w.Write(data); err != nil {
		slog.Error("Error writing config response", "error", err)
	}
}

func main() {
	rawAddress := flag.String("mac", "", "The mac address of a server")

	var config types.BootyConfig
	flag.StringVar(&config.Action, "action", "", "The action that is being performed [readImage/writeImage]")
	flag.BoolVar(&config.DryRun, "dryRun", false, "Only demonstrate the output from the actions")
	flag.BoolVar(&config.DropToShell, "shell", false, "Start a shell")
	flag.BoolVar(&config.WipeDevice, "wipe", false, "Wipe the device [OnError]")

	flag.StringVar(&config.LVMRootName, "lvmRoot", "/dev/ubuntu-vg/root", "The path to the root Linux volume")
	flag.IntVar(&config.GrowPartition, "growPartition", 1, "The partition on the destinationDevice that should be grown")

	flag.StringVar(&config.SourceImage, "sourceImage", "", "The source for the image, typically a URL")
	flag.StringVar(&config.SourceDevice, "sourceDevice", "", "The device that will be the source of the image [/dev/sda]")

	flag.StringVar(&config.DestinationAddress, "destinationAddress", "", "The destination that the image will be written to [url]")
	flag.StringVar(&config.DestinationDevice, "destinationDevice", "", "The destination device that the image will be written to [/dev/sda]")

	flag.StringVar(&config.Address, "address", "", "The network address to set on the provisioned OS [address/subnet]")
	flag.StringVar(&config.Gateway, "gateway", "", "The gateway address to be set on the provisioned OS")

	flag.Parse()

	if *rawAddress == "" {
		slog.Warn("No Mac address passed for BOOTy configuration")
	} else {
		dashmac := utils.DashMac(*rawAddress)
		http.HandleFunc(fmt.Sprintf("/booty/%s.bty", dashmac), configHandler)
		slog.Info("Handler generated", "config", dashmac+".bty")
		var err error
		data, err = json.Marshal(config)
		if err != nil {
			slog.Error("Error marshaling config", "error", err)
			os.Exit(1)
		}
	}

	switch config.Action {
	case types.ReadImage:
	case types.WriteImage:
	default:
		slog.Error("Unknown action", "action", config.Action)
		os.Exit(1)
	}

	fs := http.FileServer(http.Dir("./images"))
	http.HandleFunc("/image", imageHandler)
	http.Handle("/images/", http.StripPrefix("/images/", fs))
	slog.Info("Listening on :3000...")
	err := http.ListenAndServe(":3000", nil)
	if err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
