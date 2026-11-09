package image

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// writeBlockSize is the unit in which blockWriter compares, skips and
// punches. It is a multiple of every logical block size and of the page size.
const writeBlockSize = 64 << 10

// writeBufferSize is the read size of blockWriter. Larger buffers were not
// faster in BenchmarkStreamZstd.
const writeBufferSize = 1 << 20

var zeroBlock = make([]byte, writeBlockSize)

// punchZeroes zeroes length bytes at off of f without writing them and fails
// when f cannot guarantee that the range reads back as zeros afterwards. It is
// a variable for tests.
var punchZeroes = punchHole

// blockWriter writes an image to a device in writeBlockSize blocks and skips
// the writes that do not change what the device reads back:
//
//   - punch: an all-zero block is zeroed with punchZeroes, which on a block
//     device is a WRITE ZEROES or deallocate command instead of a data write.
//     The first failure turns punching off and writes the zeros.
//   - skipUnchanged: a block that the device already holds is not written.
//     Reinstalling the same image writes only the blocks that differ.
//
// The image checksum is computed from the stream before blockWriter, so it
// still covers every byte.
type blockWriter struct {
	dev           *os.File
	off           int64
	punch         bool
	skipUnchanged bool
	readBuf       []byte

	zeroOff, zeroLen int64 // pending zero run, punched in one call

	punched, unchanged int64
}

// writeImage copies src to dev from offset 0 and returns the bytes copied.
// Without opt.SparseWrite and opt.SkipUnchanged it is io.Copy.
func writeImage(dev *os.File, src io.Reader, opt StreamOpts) (int64, error) {
	if !opt.SparseWrite && !opt.SkipUnchanged {
		return io.Copy(dev, src) //nolint:wrapcheck // Stream wraps it
	}
	w := newBlockWriter(dev, opt)
	n, err := w.copyFrom(src)
	if err == nil {
		slog.Info("image write skipped blocks", "punched_zero_bytes", w.punched, "unchanged_bytes", w.unchanged,
			"written_bytes", n-w.punched-w.unchanged)
	}
	return n, err
}

func newBlockWriter(dev *os.File, opt StreamOpts) *blockWriter {
	w := &blockWriter{dev: dev, punch: opt.SparseWrite, skipUnchanged: opt.SkipUnchanged}
	if w.skipUnchanged {
		if err := dropCachedBlocks(dev); err != nil {
			slog.Warn("cannot read the target past its page cache, writing every block", "error", err)
			w.skipUnchanged = false
		}
		w.readBuf = make([]byte, writeBlockSize)
	}
	return w
}

func (w *blockWriter) copyFrom(src io.Reader) (int64, error) {
	buf := make([]byte, writeBufferSize)
	for {
		n, rerr := fill(src, buf)
		if err := w.write(buf[:n]); err != nil {
			return w.off, err
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return w.off, rerr //nolint:wrapcheck // checksum and download errors stay matchable
		}
	}
	return w.off, w.flushZeros()
}

// fill reads into buf until it is full or src fails. Unlike io.ReadFull it
// returns the error of src unchanged: a decompressor reports a truncated
// image as io.ErrUnexpectedEOF, which must fail the write.
func fill(src io.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := src.Read(buf[n:])
		n += m
		if err != nil {
			return n, err //nolint:wrapcheck // passed through unchanged
		}
	}
	return n, nil
}

// write writes p at the current offset. Consecutive blocks that need a data
// write go out in one WriteAt.
func (w *blockWriter) write(p []byte) error {
	dirty := -1 // start in p of the pending data write
	for i := 0; i < len(p); i += writeBlockSize {
		blk := p[i:min(i+writeBlockSize, len(p))]
		off := w.off + int64(i)
		skip, err := w.skip(blk, off)
		if err != nil {
			return err
		}
		if skip {
			if dirty >= 0 {
				if err := w.writeAt(p[dirty:i], w.off+int64(dirty)); err != nil {
					return err
				}
				dirty = -1
			}
			continue
		}
		if err := w.flushZeros(); err != nil {
			return err
		}
		if dirty < 0 {
			dirty = i
		}
	}
	if dirty >= 0 {
		if err := w.writeAt(p[dirty:], w.off+int64(dirty)); err != nil {
			return err
		}
	}
	w.off += int64(len(p))
	return nil
}

// skip reports whether blk at off needs no data write: it joins the pending
// zero run, or the device already holds it.
func (w *blockWriter) skip(blk []byte, off int64) (bool, error) {
	if w.punch && len(blk) == writeBlockSize && bytes.Equal(blk, zeroBlock) {
		joined, err := w.joinZeroRun(off)
		if joined || err != nil {
			return joined, err
		}
	}
	if !w.skipUnchanged {
		return false, nil
	}
	have := w.readBuf[:len(blk)]
	if n, err := w.dev.ReadAt(have, off); n != len(blk) || (err != nil && !errors.Is(err, io.EOF)) {
		return false, nil // unreadable or past the end: write it and let the write report errors
	}
	if !bytes.Equal(have, blk) {
		return false, nil
	}
	w.unchanged += int64(len(blk))
	return true, nil
}

// joinZeroRun adds the zero block at off to the pending zero run. A run that
// does not end at off is punched first; when that punch fails, punching is
// off and joinZeroRun returns false.
func (w *blockWriter) joinZeroRun(off int64) (bool, error) {
	if w.zeroLen > 0 && w.zeroOff+w.zeroLen != off {
		if err := w.flushZeros(); err != nil {
			return false, err
		}
	}
	if !w.punch {
		return false, nil
	}
	if w.zeroLen == 0 {
		w.zeroOff = off
	}
	w.zeroLen += writeBlockSize
	return true, nil
}

// flushZeros zeroes the pending zero run. When the punch fails it turns
// punching off and writes the zeros instead.
func (w *blockWriter) flushZeros() error {
	if w.zeroLen == 0 {
		return nil
	}
	off, n := w.zeroOff, w.zeroLen
	w.zeroLen = 0
	err := punchZeroes(w.dev, off, n)
	if err == nil {
		w.punched += n
		return nil
	}
	slog.Info("hole punch failed, writing zeros from here on", "error", err)
	w.punch = false
	for done := int64(0); done < n; done += writeBlockSize {
		if err := w.writeAt(zeroBlock, off+done); err != nil {
			return err
		}
	}
	return nil
}

func (w *blockWriter) writeAt(p []byte, off int64) error {
	if _, err := w.dev.WriteAt(p, off); err != nil {
		return fmt.Errorf("writing at offset %d: %w", off, err)
	}
	return nil
}
