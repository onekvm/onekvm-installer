package installer

import (
	"context"
	"fmt"
)

type directMachine interface {
	InstallDirect(context.Context, Device, string, *ReleaseAsset, []byte, Reporter) (AccessInfo, error)
}

func InstallDirect(ctx context.Context, d Device, source string, asset *ReleaseAsset, firstboot []byte, report Reporter) (AccessInfo, error) {
	machine, err := MachineFor(d.Profile)
	if err != nil {
		return AccessInfo{}, err
	}
	direct, ok := machine.(directMachine)
	if !ok || !DirectInstall(d) {
		return AccessInfo{}, fmt.Errorf("direct installation is unavailable for this target")
	}
	if report == nil {
		report = Report
	}
	return direct.InstallDirect(ctx, d, source, asset, firstboot, report)
}

// GitHub filenames select a candidate; the payload metadata remains decisive.
func ReleaseProfile(name string) string {
	for _, m := range Machines() {
		if m.MatchesImage(name) {
			return m.Profile()
		}
	}
	return ""
}

func CandidateImages(rel Release, profile string) []ReleaseAsset {
	selected, err := MachineFor(profile)
	if err != nil {
		return nil
	}
	var out []ReleaseAsset
	for _, asset := range rel.Assets {
		candidate, err := MachineFor(ReleaseProfile(asset.Name))
		if err == nil && candidate.Family() == selected.Family() {
			out = append(out, asset)
		}
	}
	return out
}
