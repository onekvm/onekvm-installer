package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// copyRawImage copies the published .img unchanged and checks its exact length.
// The same operation is used for local staging and the complete RAM copy.
func copyRawImage(dst *os.File, src io.Reader, size int64, progress Progress) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("invalid raw image size")
	}
	h := sha256.New()
	if progress != nil {
		progress(0, size)
	}
	n, err := CopyProgress(io.MultiWriter(dst, h), io.LimitReader(src, size), size, progress)
	if err != nil {
		return "", err
	}
	if n != size {
		return "", fmt.Errorf("incomplete raw image")
	}
	var extra [1]byte
	if n, err := io.ReadFull(src, extra[:]); n != 0 || err != io.EOF {
		return "", fmt.Errorf("raw image length changed")
	}
	if err := dst.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func stageLocalImage(source, destination string, size int64, progress Progress) error {
	r, err := OpenImage(source)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = copyRawImage(f, r, size, progress)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
