package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onekvm/onekvm-installer/internal/buildinfo"
)

type ReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type Release struct {
	Tag    string         `json:"tag_name"`
	Assets []ReleaseAsset `json:"assets"`
}
type Catalog map[string]string

var DefaultCatalog = Catalog{}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func LoadCatalog(path string) (Catalog, error) {
	c := Catalog{}
	for k, v := range DefaultCatalog {
		c[k] = v
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
	}
	for _, v := range c {
		if !repoPattern.MatchString(v) {
			return nil, fmt.Errorf("invalid GitHub repository %q", v)
		}
	}
	return c, nil
}

func httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || req.URL.Scheme != "https" {
			return fmt.Errorf("unsafe or excessive redirect")
		}
		return nil
	}}
}
func githubRequest(ctx context.Context, url string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", "onekvm-installer/"+buildinfo.Version)
	if strings.HasPrefix(url, "https://api.github.com/") {
		r.Header.Set("Accept", "application/vnd.github+json")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return r, nil
}
func FetchRelease(ctx context.Context, repository, tag string) (Release, error) {
	var rel Release
	if !repoPattern.MatchString(repository) {
		return rel, fmt.Errorf("invalid GitHub repository")
	}
	endpoint := "https://api.github.com/repos/" + repository + "/releases/latest"
	if tag != "" && tag != "latest" {
		endpoint = "https://api.github.com/repos/" + repository + "/releases/tags/" + url.PathEscape(tag)
	}
	req, err := githubRequest(ctx, endpoint)
	if err != nil {
		return rel, err
	}
	resp, err := httpClient(30 * time.Second).Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return rel, fmt.Errorf("GitHub API: %s (check repository, tag or rate limit)", resp.Status)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4*MiB)).Decode(&rel)
	return rel, err
}

func ImageAssets(rel Release, profile string) []ReleaseAsset {
	var out []ReleaseAsset
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if !strings.HasSuffix(n, ".img") {
			continue
		}
		match := false
		if machine, err := MachineFor(profile); err == nil {
			match = machine.MatchesImage(n)
		}

		if match {
			out = append(out, a)
		}
	}
	return out
}

func Download(ctx context.Context, a ReleaseAsset, destination string, maxBytes int64, progress Progress) error {
	if a.Size <= 0 || a.Size > maxBytes {
		return fmt.Errorf("release asset exceeds staging capacity")
	}
	body, err := OpenReleaseStream(ctx, a)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := CopyProgress(io.MultiWriter(f, h), io.LimitReader(body, a.Size+1), a.Size, progress)
	if err != nil {
		return err
	}
	if n != a.Size {
		return fmt.Errorf("incomplete or oversized download")
	}
	if a.Digest != "" && a.Digest != "sha256:"+hex.EncodeToString(h.Sum(nil)) {
		return fmt.Errorf("GitHub asset SHA-256 mismatch")
	}
	return f.Sync()
}

func OpenReleaseStream(ctx context.Context, a ReleaseAsset) (io.ReadCloser, error) {
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return nil, fmt.Errorf("release download must start at https://github.com")
	}
	req, err := githubRequest(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient(2 * time.Hour).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	return resp.Body, nil
}
