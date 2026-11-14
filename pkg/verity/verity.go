// Package verity writes dm-verity hash trees in the format of veritysetup
// (format 1, SHA-256, 4 KiB blocks, with superblock), so systemd and
// libcryptsetup can open them like the output of veritysetup format.
package verity

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	blockSize = 4096
	saltSize  = 32
	// hashesPerBlock is the number of SHA-256 digests in one hash block.
	hashesPerBlock = blockSize / sha256.Size
)

// Format writes the hash tree of the data file to hashPath and returns the
// root hash as lower-case hex, the form that dm-verity tables, systemd
// .roothash files and root hash signatures use. The data file size must be
// a multiple of 4096 and at least 8 KiB.
func Format(dataPath, hashPath string) (string, error) {
	data, err := os.Open(dataPath)
	if err != nil {
		return "", fmt.Errorf("open verity data: %w", err)
	}
	defer func() { _ = data.Close() }()
	info, err := data.Stat()
	if err != nil {
		return "", fmt.Errorf("stat verity data: %w", err)
	}
	// One block would have no hash level (veritysetup hashes it directly).
	if info.Size() < 2*blockSize || info.Size()%blockSize != 0 {
		return "", fmt.Errorf("verity data size %d is not a multiple of %d of at least two blocks", info.Size(), blockSize)
	}
	salt := make([]byte, saltSize)
	uuid := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("verity salt: %w", err)
	}
	if _, err := rand.Read(uuid); err != nil {
		return "", fmt.Errorf("verity uuid: %w", err)
	}
	uuid[6] = uuid[6]&0x0f | 0x40 // RFC 4122 version 4
	uuid[8] = uuid[8]&0x3f | 0x80

	// Level 0 hashes the data blocks, each next level the blocks of the one
	// below, up to a level of one block, whose hash is the root hash.
	hashes, err := hashBlocks(salt, data)
	if err != nil {
		return "", err
	}
	var levels [][]byte
	for {
		level := packHashes(hashes)
		levels = append(levels, level)
		if hashes, err = hashBlocks(salt, bytes.NewReader(level)); err != nil {
			return "", err
		}
		if len(level) == blockSize {
			break
		}
	}
	out := superblock(uint64(info.Size()/blockSize), salt, uuid)
	// The top level comes first, right after the superblock block.
	for i := len(levels) - 1; i >= 0; i-- {
		out = append(out, levels[i]...)
	}
	if err := os.WriteFile(hashPath, out, 0o644); err != nil { //nolint:gosec // public hash tree
		return "", fmt.Errorf("write verity hash tree: %w", err)
	}
	return hex.EncodeToString(hashes[0]), nil
}

// hashBlocks returns SHA-256(salt || block) of every block of r.
func hashBlocks(salt []byte, r io.Reader) ([][]byte, error) {
	var out [][]byte
	block := make([]byte, blockSize)
	for {
		if _, err := io.ReadFull(r, block); errors.Is(err, io.EOF) {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("read verity data: %w", err)
		}
		h := sha256.New()
		h.Write(salt)
		h.Write(block)
		out = append(out, h.Sum(nil))
	}
}

// packHashes puts the hashes into blocks, the last one padded with zeros.
func packHashes(hashes [][]byte) []byte {
	n := (len(hashes) + hashesPerBlock - 1) / hashesPerBlock
	out := make([]byte, 0, n*blockSize)
	for _, h := range hashes {
		out = append(out, h...)
	}
	return append(out, make([]byte, n*blockSize-len(out))...)
}

// superblock returns struct verity_sb of cryptsetup, padded to one block.
func superblock(dataBlocks uint64, salt, uuid []byte) []byte {
	sb := make([]byte, blockSize)
	copy(sb, "verity")
	binary.LittleEndian.PutUint32(sb[8:], 1)  // version
	binary.LittleEndian.PutUint32(sb[12:], 1) // hash type: normal
	copy(sb[16:32], uuid)
	copy(sb[32:64], "sha256")
	binary.LittleEndian.PutUint32(sb[64:], blockSize) // data block size
	binary.LittleEndian.PutUint32(sb[68:], blockSize) // hash block size
	binary.LittleEndian.PutUint64(sb[72:], dataBlocks)
	binary.LittleEndian.PutUint16(sb[80:], uint16(len(salt))) //nolint:gosec // saltSize
	copy(sb[88:], salt)
	return sb
}
