package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrActiveInstallation = errors.New("recovery boot replacement has started; keep the staged installation files")
var ErrStagedSource = errors.New("select an image outside the installer staging directory")

// Rebuild only our private cache, using Go filesystem APIs throughout. Armed
// requests and boot markers must retain the image needed to finish installation.
func ResetStaging(d Device, source string) error {
	for _, name := range []string{MarkerName, "onekvm-install.started.json"} {
		_, err := os.Lstat(filepath.Join(d.BootMount, name))
		if err == nil {
			return ErrActiveInstallation
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	dir := StageDir(d)
	// The protection also applies to requests from older installer protocols.
	var previous struct {
		State string `json:"state"`
	}
	previousData, readErr := os.ReadFile(filepath.Join(dir, "request.json"))
	if readErr == nil && json.Unmarshal(previousData, &previous) == nil && (previous.State == "arming" || previous.State == "armed" || previous.State == "writing") {
		return ErrActiveInstallation
	}
	if source != "" {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
		data, err := filepath.EvalSymlinks(d.DataMount)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Join(data, StageName), resolved)
		if err != nil {
			return err
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrStagedSource
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clean previous staging files: %w", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	return nil
}
