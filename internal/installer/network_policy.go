package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const NetworkPolicyPrefix = "# onekvm-network-policy-v1 "

type NetworkPolicy struct {
	Version  int      `json:"version"`
	Disabled []string `json:"disabled"`
}

func (p *NetworkPolicy) Validate() error {
	if p == nil {
		return nil
	}
	known := map[string]bool{"internet": true, "ntp": true, "dns": true, "discovery": true, "stun": true, "turn": true, "acme": true, "update": true, "extensions": true, "mdns": true}
	seen := map[string]bool{}
	if p.Version != 1 || p.Disabled == nil || len(p.Disabled) > len(known) {
		return fmt.Errorf("invalid network policy version or size")
	}
	for _, feature := range p.Disabled {
		if !known[feature] || seen[feature] {
			return fmt.Errorf("invalid network policy feature %q", feature)
		}
		seen[feature] = true
	}
	return nil
}

func ParseNetworkPolicy(firstboot []byte) (*NetworkPolicy, error) {
	var result *NetworkPolicy
	for _, line := range strings.Split(strings.TrimPrefix(string(firstboot), "\ufeff"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# onekvm-network-policy-") && !strings.HasPrefix(strings.TrimSpace(line), NetworkPolicyPrefix) {
			return nil, fmt.Errorf("unsupported network policy envelope")
		}
		if raw, ok := strings.CutPrefix(strings.TrimSpace(line), NetworkPolicyPrefix); ok {
			if result != nil {
				return nil, fmt.Errorf("duplicate network policy")
			}
			var policy NetworkPolicy
			if err := decodeNetworkPolicy([]byte(raw), &policy); err != nil {
				return nil, err
			}
			result = &policy
		}
	}
	return result, nil
}

func decodeNetworkPolicy(data []byte, policy *NetworkPolicy) error {
	if len(data) > 16384 {
		return fmt.Errorf("network policy exceeds 16 KiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(policy); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("unexpected data after network policy")
	}
	return policy.Validate()
}

func CheckNetworkPolicy(image Image, policy *NetworkPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if policy != nil && len(policy.Disabled) > 0 && !image.NetworkPolicy {
		return fmt.Errorf("this image does not support installer network policy; choose a compatible image or allow all network features")
	}
	return nil
}
