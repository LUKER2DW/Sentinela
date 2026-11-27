// Package apk reads who signed an Android APK, without running anything
// from it: the signer certificates of its APK Signature Scheme v2, v3 and
// v3.1 blocks (the APK Signing Block, just before the ZIP central
// directory). It does not verify the signatures: Android does that before
// installing, and refuses an APK whose block names a certificate the
// signature does not match. What this answers is which key Android will
// see as the app's signer, so a server can refuse to offer an APK signed
// by anyone but the app's own key (internal/api/app_mirror.go).
package apk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Block IDs of the signature schemes whose signers Android checks.
const (
	idV2  = 0x7109871a
	idV3  = 0xf05368c0
	idV31 = 0x1b93ad61
)

const (
	eocdSig     = 0x06054b50
	eocdMin     = 22
	maxComment  = 0xffff
	blockMagic  = "APK Sig Block 42"
	maxSigBlock = 16 << 20 // a few KB in practice
	maxSigners  = 16
	maxCerts    = 16
)

// Signers returns the SHA-256 (hex) of the certificate of every signer in
// the APK's v2, v3 and v3.1 blocks, without duplicates. An APK with none of
// those blocks (v1 only, or not an APK) is an error: since Android 7 the
// app's v2/v3 signer is what an update must match.
func Signers(r io.ReaderAt, size int64) ([]string, error) {
	cdOff, err := centralDirectory(r, size)
	if err != nil {
		return nil, err
	}
	block, err := signingBlock(r, cdOff)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	found := false
	for len(block) > 0 {
		if len(block) < 12 {
			return nil, errors.New("apk: truncated signing block entry")
		}
		n := binary.LittleEndian.Uint64(block)
		if n < 4 || n > uint64(len(block)-8) {
			return nil, errors.New("apk: bad signing block entry")
		}
		id := binary.LittleEndian.Uint32(block[8:])
		value := block[12 : 8+n]
		block = block[8+n:]
		if id != idV2 && id != idV3 && id != idV31 {
			continue
		}
		found = true
		certs, err := schemeSigners(value)
		if err != nil {
			return nil, fmt.Errorf("apk: scheme block %#x: %w", id, err)
		}
		for _, c := range certs {
			sum := sha256.Sum256(c)
			h := hex.EncodeToString(sum[:])
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	if !found {
		return nil, errors.New("apk: no v2 or v3 signature")
	}
	if len(out) == 0 {
		return nil, errors.New("apk: no signer certificate")
	}
	return out, nil
}

// centralDirectory finds the End of Central Directory record and returns
// the central directory's offset.
func centralDirectory(r io.ReaderAt, size int64) (int64, error) {
	if size < eocdMin {
		return 0, errors.New("apk: not a ZIP file")
	}
	n := int64(eocdMin + maxComment)
	if n > size {
		n = size
	}
	tail := make([]byte, n)
	if _, err := r.ReadAt(tail, size-n); err != nil && err != io.EOF {
		return 0, fmt.Errorf("apk: %w", err)
	}
	for i := len(tail) - eocdMin; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) != eocdSig {
			continue
		}
		// The comment must run exactly to the end of the file.
		if int(binary.LittleEndian.Uint16(tail[i+20:])) != len(tail)-i-eocdMin {
			continue
		}
		cdSize := int64(binary.LittleEndian.Uint32(tail[i+12:]))
		cdOff := int64(binary.LittleEndian.Uint32(tail[i+16:]))
		eocdOff := size - n + int64(i)
		if cdOff == 0xffffffff || cdOff+cdSize != eocdOff {
			return 0, errors.New("apk: unsupported or inconsistent central directory")
		}
		return cdOff, nil
	}
	return 0, errors.New("apk: no end of central directory")
}

// signingBlock returns the ID-value pairs of the APK Signing Block that
// ends right before the central directory.
func signingBlock(r io.ReaderAt, cdOff int64) ([]byte, error) {
	if cdOff < 32 {
		return nil, errors.New("apk: no signing block")
	}
	var foot [24]byte
	if _, err := r.ReadAt(foot[:], cdOff-24); err != nil {
		return nil, fmt.Errorf("apk: %w", err)
	}
	if !bytes.Equal(foot[8:], []byte(blockMagic)) {
		return nil, errors.New("apk: no signing block")
	}
	n := binary.LittleEndian.Uint64(foot[:8]) // size of the block minus its first size field
	if n < 24 || n > maxSigBlock || int64(n)+8 > cdOff {
		return nil, errors.New("apk: bad signing block size")
	}
	start := cdOff - int64(n) - 8
	b := make([]byte, n+8)
	if _, err := r.ReadAt(b, start); err != nil {
		return nil, fmt.Errorf("apk: %w", err)
	}
	if binary.LittleEndian.Uint64(b) != n {
		return nil, errors.New("apk: signing block sizes differ")
	}
	return b[8 : len(b)-24], nil
}

// schemeSigners returns the first certificate of each signer of a v2 or v3
// block (the signer's own; any others are its chain). Both schemes put the
// certificates second in the signed data.
func schemeSigners(value []byte) ([][]byte, error) {
	signers, _, err := lp(value)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for count := 0; len(signers) > 0; count++ {
		if count >= maxSigners {
			return nil, errors.New("too many signers")
		}
		var signer []byte
		if signer, signers, err = lp(signers); err != nil {
			return nil, err
		}
		signed, _, err := lp(signer)
		if err != nil {
			return nil, err
		}
		// signed data: digests, certificates, (v3: min/max SDK,) attributes
		_, rest, err := lp(signed)
		if err != nil {
			return nil, err
		}
		certs, _, err := lp(rest)
		if err != nil {
			return nil, err
		}
		first, _, err := lp(certs)
		if err != nil || len(first) == 0 {
			return nil, errors.New("signer without a certificate")
		}
		out = append(out, first)
		if len(out) > maxCerts {
			return nil, errors.New("too many certificates")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no signer")
	}
	return out, nil
}

// lp splits a uint32 little-endian length-prefixed value off b.
func lp(b []byte) (value, rest []byte, err error) {
	if len(b) < 4 {
		return nil, nil, errors.New("truncated length prefix")
	}
	n := binary.LittleEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, errors.New("length prefix past the end")
	}
	return b[4 : 4+n], b[4+n:], nil
}
