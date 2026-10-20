package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// Pro eMMC preserves the vendor boot prefix, including SPL/U-Boot and the
// environment. SD writes start at zero. Metadata is never written as payload.
func WriteVerifiedRange(target io.ReadWriteSeeker, source io.Reader, info Image, skip int64, p Progress) error {
	if skip < 0 || info.PayloadSize <= skip {
		return fmt.Errorf("invalid image payload range")
	}
	fullHash := sha256.New()
	writtenHash := sha256.New()
	if _, err := io.CopyN(fullHash, source, skip); err != nil {
		return err
	}
	if _, err := target.Seek(skip, io.SeekStart); err != nil {
		return err
	}
	size := info.PayloadSize - skip
	n, err := CopyProgress(io.MultiWriter(target, fullHash, writtenHash), io.LimitReader(source, size), size, p)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(fullHash.Sum(nil)) != info.PayloadSHA256 {
		return fmt.Errorf("incomplete image write / source changed")
	}
	if f, ok := target.(interface{ Sync() error }); ok {
		if err = f.Sync(); err != nil {
			return err
		}
	}
	if _, err = target.Seek(skip, io.SeekStart); err != nil {
		return err
	}
	readHash := sha256.New()
	n, err = CopyProgress(readHash, io.LimitReader(target, size), size, p)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(readHash.Sum(nil)) != hex.EncodeToString(writtenHash.Sum(nil)) {
		return fmt.Errorf("disk readback SHA-256 mismatch")
	}
	return nil
}
