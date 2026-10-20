package installer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Request struct {
	Version         int               `json:"version"`
	Token           string            `json:"token"`
	State           string            `json:"state"`
	Device          Device            `json:"device"`
	Image           Image             `json:"image"`
	ImageBytes      int64             `json:"image_bytes"`
	FileSHA256      string            `json:"file_sha256"`
	FirstbootSHA256 string            `json:"firstboot_sha256"`
	KernelSHA256    string            `json:"kernel_sha256"`
	EnvSHA256       string            `json:"env_sha256"`
	BootFiles       map[string]string `json:"boot_files,omitempty"`
}
type Marker struct {
	Version       int    `json:"version"`
	Token         string `json:"token"`
	Device        Device `json:"device"`
	RequestSHA256 string `json:"request_sha256"`
}
type Reporter func(label string, operation func(Progress) error) error

func Report(label string, operation func(Progress) error) error { return operation(nil) }

func freeBytes(path string) (int64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return int64(s.Bavail) * int64(s.Bsize), nil
}
func StageDir(d Device) string { return filepath.Join(d.DataMount, StageName) }

func Prepare(ctx context.Context, d Device, source string, asset *ReleaseAsset, firstboot []byte, report Reporter) (req Request, err error) {
	return prepare(ctx, d, source, asset, nil, firstboot, report)
}

func PrepareDownloaded(ctx context.Context, d Device, image *DownloadedImage, firstboot []byte, report Reporter) (Request, error) {
	if image == nil || !SameDisk(d, image.device) || image.Image.Profile != d.Profile {
		return Request{}, fmt.Errorf("download does not match the selected target")
	}
	return prepare(ctx, d, image.Source, nil, image, firstboot, report)
}

func prepare(ctx context.Context, d Device, source string, asset *ReleaseAsset, downloaded *DownloadedImage, firstboot []byte, report Reporter) (req Request, err error) {
	if DirectInstall(d) {
		return req, fmt.Errorf("direct installation must not create a staging directory")
	}
	if err = CheckInstallTarget("/"); err != nil {
		return req, err
	}
	if report == nil {
		report = Report
	}
	if err = d.CanArm(); err != nil {
		return req, err
	}
	current, err := ProbeTarget("/", d, d.Profile)
	if err != nil {
		return req, err
	}
	if !SameDisk(d, current) {
		return req, fmt.Errorf("storage changed since detection")
	}
	limit := min(d.Bytes, 4*1024*MiB)
	required := int64(256 * MiB)
	if asset != nil {
		required += asset.Size
	} else if downloaded == nil {
		s, err := os.Stat(source)
		if err != nil {
			return req, err
		}
		if !s.Mode().IsRegular() {
			return req, fmt.Errorf("local image must be a regular file")
		}
		required += s.Size()
	}
	err = report("op.cleanup", func(Progress) error {
		localSource := source
		if asset != nil {
			localSource = ""
		}
		return ResetStaging(d, localSource)
	})
	if err != nil {
		return req, err
	}
	free, err := freeBytes(d.DataMount)
	if err != nil {
		return req, err
	}
	if free < required {
		return req, fmt.Errorf("userdata needs at least %d MiB free", required/MiB)
	}
	dir := StageDir(d)
	// Only a complete request.json may arm; a retry rebuilds this directory.
	if asset != nil {
		source = filepath.Join(dir, "image.img")
		err = report("Downloading full image to userdata", func(p Progress) error { return Download(ctx, *asset, source, min(free-256*MiB, limit), p) })
		if err != nil {
			return req, err
		}
	}
	var info Image
	err = report("Validating image metadata and SHA-256", func(p Progress) error {
		var e error
		info, e = VerifyContext(ctx, source, d.Profile, limit, p)
		if e == nil && downloaded != nil && info != downloaded.Image {
			e = fmt.Errorf("downloaded image changed since validation")
		}
		return e
	})
	if err != nil {
		return req, err
	}
	mem, err := Memory("MemTotal")
	if err != nil {
		return req, err
	}
	if info.PayloadSize+512+RAMReserve > mem {
		return req, fmt.Errorf("image needs %d MiB RAM plus %d MiB recovery reserve; device has %d MiB", (info.PayloadSize+512)/MiB, RAMReserve/MiB, mem/MiB)
	}
	staged := filepath.Join(dir, "image.img")
	if downloaded != nil {
		// The download is already on this userdata filesystem. Move the exact
		// verified file into the request instead of downloading or copying it.
		if err = ctx.Err(); err != nil {
			return req, err
		}
		if err = downloaded.moveTo(d, info, staged); err != nil {
			return req, err
		}
	} else if asset == nil {
		err = report("op.stage", func(p Progress) error { return stageLocalImage(source, staged, info.PayloadSize+512, p) })
		if err != nil {
			return req, err
		}
		err = report("op.staged_verify", func(p Progress) error {
			copied, e := Verify(staged, d.Profile, limit, p)
			if e == nil && copied != info {
				e = fmt.Errorf("staged image differs from verified source")
			}
			return e
		})
		if err != nil {
			return req, err
		}
	}
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return req, err
	}
	err = report("Extracting installer recovery kernel", func(p Progress) error { return machine.ExtractBootstrap(staged, dir, p) })
	if err != nil {
		return req, err
	}
	err = report("op.finalize", func(p Progress) error {
		if err = WritePrivate(filepath.Join(dir, "firstboot.txt"), firstboot); err != nil {
			return err
		}
		token := make([]byte, 32)
		if _, err = rand.Read(token); err != nil {
			return err
		}
		req = Request{Version: RecoveryAPI, Token: hex.EncodeToString(token), State: "prepared", Device: d, Image: info, ImageBytes: info.PayloadSize + 512}
		if err = machine.PrepareBootstrap(dir, req); err != nil {
			return err
		}
		if err = RequestHashes(&req, dir, false, p); err != nil {
			return err
		}
		if err = WriteJSON(filepath.Join(dir, "request.json"), req); err != nil {
			return err
		}
		return nil
	})
	return req, err
}

func LoadPrepared(d Device, progress Progress) (Request, error) {
	var req Request
	err := ReadJSON(filepath.Join(StageDir(d), "request.json"), &req)
	if err != nil {
		return req, err
	}
	if req.Version != RecoveryAPI || req.ImageBytes != req.Image.PayloadSize+512 || len(req.Token) != 64 || !SameDisk(d, req.Device) || (req.State != "prepared" && req.State != "arming" && req.State != "armed") {
		return req, fmt.Errorf("installation request does not match this device / state")
	}
	if err := RequestHashes(&req, StageDir(d), true, progress); err != nil {
		return req, err
	}
	return req, nil
}

func RequestHashes(req *Request, dir string, verify bool, progress Progress) error {
	files := []struct {
		name string
		hash *string
	}{
		{"image.img", &req.FileSHA256}, {"kernel.itb", &req.KernelSHA256},
		{"firstboot.txt", &req.FirstbootSHA256}, {"uEnv.txt", &req.EnvSHA256},
	}
	machine, machineErr := MachineFor(req.Device.Profile)
	if machineErr == nil && len(machine.BootstrapFiles()) > 0 {
		names := machine.BootstrapFiles()
		if !verify {
			req.BootFiles = map[string]string{}
		} else if len(req.BootFiles) != len(names) {
			return fmt.Errorf("invalid bootstrap hash inventory")
		}
		files = []struct {
			name string
			hash *string
		}{{"image.img", &req.FileSHA256}, {"firstboot.txt", &req.FirstbootSHA256}}
		for _, name := range names {
			hash, err := HashFile(filepath.Join(dir, name), nil)
			if err != nil {
				return err
			}
			if verify && req.BootFiles[name] != hash {
				return fmt.Errorf("staged %s changed", name)
			}
			req.BootFiles[name] = hash
		}
	} else if len(req.BootFiles) > 0 {
		return fmt.Errorf("unexpected bootstrap hash inventory")
	}

	var total, offset int64
	for _, file := range files {
		s, err := os.Stat(filepath.Join(dir, file.name))
		if err != nil {
			return err
		}
		total += s.Size()
	}
	if progress != nil {
		progress(0, total)
	}
	for _, file := range files {
		hash, err := HashFile(filepath.Join(dir, file.name), func(done, _ int64) {
			if progress != nil {
				progress(offset+done, total)
			}
		})
		if err != nil {
			return err
		}
		if verify && hash != *file.hash {
			return fmt.Errorf("staged %s changed", file.name)
		}
		*file.hash = hash
		s, err := os.Stat(filepath.Join(dir, file.name))
		if err != nil {
			return err
		}
		offset += s.Size()
	}
	return nil
}

// Arm validates everything before replacing the stock boot files. Once cleanup
// starts, userdata must be retained and only finishing this installation is
// supported: the old boot files are deliberately not backed up.
func Arm(d Device, report Reporter) (AccessInfo, error) {
	if err := CheckInstallTarget("/"); err != nil {
		return AccessInfo{}, err
	}
	if err := d.CanArm(); err != nil {
		return AccessInfo{}, err
	}
	if report == nil {
		report = Report
	}
	var req Request
	err := report("Rechecking staged image and configuration", func(p Progress) error {
		var e error
		req, e = LoadPrepared(d, p)
		return e
	})
	if err != nil {
		return AccessInfo{}, err
	}
	if req.State == "armed" {
		return AccessInfo{}, fmt.Errorf("installation already armed; reboot to install")
	}
	access, err := preparedAccess(StageDir(d), req.FirstbootSHA256)
	if err != nil {
		return AccessInfo{}, err
	}
	mem, err := Memory("MemTotal")
	if err != nil {
		return AccessInfo{}, err
	}
	if req.ImageBytes+RAMReserve > mem {
		return AccessInfo{}, fmt.Errorf("insufficient recovery RAM")
	}
	current, err := ProbeTarget("/", d, d.Profile)
	if err != nil {
		return AccessInfo{}, err
	}
	if !SameDisk(d, current) {
		return AccessInfo{}, fmt.Errorf("boot medium changed")
	}
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return AccessInfo{}, err
	}
	if err := machine.ActivateRecovery(d, req, report); err != nil {
		return AccessInfo{}, err
	}
	return access, nil
}
