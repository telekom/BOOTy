package image

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
)

// WriteCounter counts the number of bytes written to it. It implements to the io.Writer interface
// and we can pass this into io.TeeReader() which will report progress on each write cycle.
type WriteCounter struct {
	Total atomic.Uint64
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	n := len(p)
	wc.Total.Add(uint64(n))
	return n, nil
}

func tickerProgress(byteCounter uint64) {
	// Clear the line by using a character return to go back to the start and remove
	// the remaining characters by filling it with spaces
	fmt.Printf("\r%s", strings.Repeat(" ", 35))

	// Return again and print current status of download
	// We use the humanize package to print the bytes in a meaningful way (e.g. 10 MB)
	fmt.Printf("\rDownloading... %s complete", humanize.Bytes(byteCounter))
}

// Read will take a local disk and copy an image to a remote server.
func Read(sourceDevice, destinationAddress, mac string, compressed bool) error {

	var fileName string
	if !compressed {
		// raw image
		fileName = fmt.Sprintf("%s.img", mac)
	} else {
		// compressed image
		fileName = fmt.Sprintf("%s.zmg", mac)
	}

	fmt.Println("--------------------------------------------------------------------------------")

	fmt.Printf("\nReading of disk [%s], and sending to [%s]\n", filepath.Base(sourceDevice), destinationAddress)
	fmt.Println("--------------------------------------------------------------------------------")

	client := &http.Client{}
	resp, err := UploadMultipartFile(client, destinationAddress, fileName, sourceDevice, compressed)
	if err != nil {
		return err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	return nil
}

// UploadMultipartFile uploads the contents of path as a multipart form file.
func UploadMultipartFile(client *http.Client, uri, key, path string, compressed bool) (*http.Response, error) {
	body, writer := io.Pipe()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, uri, body)
	if err != nil {
		return nil, fmt.Errorf("creating upload request: %w", err)
	}

	mwriter := multipart.NewWriter(writer)
	req.Header.Add("Content-Type", mwriter.FormDataContentType())

	errchan := make(chan error)

	// GO routine for the copy operation
	go func() {
		defer close(errchan)
		defer func() { _ = writer.Close() }()
		defer func() { _ = mwriter.Close() }()

		// BootyImage is the key that the client will lookfor and
		// key is the renamed file
		w, err := mwriter.CreateFormFile("BootyImage", key)
		if err != nil {
			errchan <- err
			return
		}

		diskIn, err := os.Open(path)
		if err != nil {
			errchan <- err
			return
		}

		defer func() { _ = diskIn.Close() }()

		if !compressed {
			// Without compression read raw output
			if written, err := io.Copy(w, diskIn); err != nil {
				errchan <- fmt.Errorf("error copying %s (%d bytes written): %w", path, written, err)
				return
			}

		} else {
			// With compression run data through gzip writer
			zipWriter := gzip.NewWriter(w)

			// run an io.Copy on the disk into the zipWriter
			if written, err := io.Copy(zipWriter, diskIn); err != nil {
				errchan <- fmt.Errorf("error copying %s (%d bytes written): %w", path, written, err)
				return
			}
			// Ensure we close our zipWriter (otherwise we will get "unexpected EOF")
			_ = zipWriter.Close()

		}

		if err := mwriter.Close(); err != nil {
			errchan <- err
			return
		}

	}()

	resp, err := client.Do(req) //nolint:gosec // URI is passed from caller, intentional
	merr := <-errchan

	if err != nil || merr != nil {
		return resp, errors.Join(err, merr)
	}

	return resp, nil
}

// Write will pull an image and write it to local storage device.
// With compress set to true it will use gzip compression to expand the data before
// writing to an underlying device.
func Write(sourceImage, destinationDevice string, compressed bool) error {

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, sourceImage, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating image request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // sourceImage URL is from trusted config
	if err != nil {
		return fmt.Errorf("fetching image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		// Customize response for the 404 to make debugging simpler.
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%s not found", sourceImage)
		}
		return fmt.Errorf("%s", resp.Status)
	}

	var out io.Reader

	fileOut, err := os.OpenFile(destinationDevice, os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // device files need world-readable permissions
	if err != nil {
		return fmt.Errorf("opening destination device: %w", err)
	}
	defer func() { _ = fileOut.Close() }()

	if !compressed {
		// Without compression send raw output
		out = resp.Body
	} else {
		// With compression run data through gzip reader
		zipOUT, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("new gzip reader: %w", err)
		}
		defer func() { _ = zipOUT.Close() }()
		out = zipOUT
	}

	slog.Info("Beginning write of image to disk", "image", filepath.Base(sourceImage), "device", destinationDevice)
	// Create our progress reporter and pass it to be used alongside our writer
	ticker := time.NewTicker(500 * time.Millisecond)
	counter := &WriteCounter{}

	go func() {
		for ; true; <-ticker.C {
			tickerProgress(counter.Total.Load())
		}
	}()
	if _, err = io.Copy(fileOut, io.TeeReader(out, counter)); err != nil {
		ticker.Stop()
		return fmt.Errorf("writing image to disk: %w", err)
	}

	fmt.Printf("\n")
	ticker.Stop()
	return nil
}
