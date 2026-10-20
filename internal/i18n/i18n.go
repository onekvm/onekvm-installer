// Package i18n owns the installer's embedded, offline message catalogs.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var files embed.FS

type Translator struct {
	Language string
	messages map[string]string
}

// New honors an explicit language, then POSIX message locale precedence.
// Unsupported system locales fall back to English; explicit typos are errors.
func New(language string, getenv func(string) string) (Translator, error) {
	if language == "" || language == "auto" {
		language = "en"
		for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if locale := getenv(name); locale != "" {
				if supported := catalogLanguage(locale); supported != "" {
					language = supported
				} else if strings.HasPrefix(strings.ToLower(locale), "zh") {
					language = "zh"
				}
				break
			}
		}
	} else {
		if supported := catalogLanguage(language); supported != "" {
			language = supported
		} else {
			return Translator{}, fmt.Errorf("unsupported language %q (use auto, en, zh or zh-TW)", language)
		}
	}
	data, err := files.ReadFile("locales/" + language + ".json")
	if err != nil {
		return Translator{}, err
	}
	t := Translator{Language: language}
	err = json.Unmarshal(data, &t.messages)
	return t, err
}

// Accept POSIX locale names and common Chinese region/script aliases.
func catalogLanguage(locale string) string {
	locale = strings.ToLower(strings.ReplaceAll(locale, "_", "-"))
	locale, _, _ = strings.Cut(locale, ".")
	locale, _, _ = strings.Cut(locale, "@")
	switch locale {
	case "en", "en-us", "en-gb":
		return "en"
	case "zh", "zh-cn", "zh-sg", "zh-hans", "zh-hans-cn", "zh-hans-sg":
		return "zh"
	case "zh-tw", "zh-hk", "zh-mo", "zh-hant", "zh-hant-tw", "zh-hant-hk", "zh-hant-mo":
		return "zh-TW"
	default:
		return ""
	}
}

func (t Translator) T(key string, args ...any) string {
	message, ok := t.messages[key]
	if !ok {
		message = key
	}
	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}

// Diagnostic translates common actionable errors; OS/tool diagnostics retain
// their original details so users can report the exact failure.
func (t Translator) Diagnostic(message string) string {
	keys := map[string]string{
		"only raw .img images are supported":                                        "diagnostic.raw",
		"select an image before checking solutions":                                 "diagnostic.solution_image",
		"insert an SD card before selecting a Pro SD image":                         "diagnostic.sd_missing",
		"invalid hostname":                                                          "diagnostic.hostname",
		"static IPv4 address must include a prefix, e.g. 192.168.1.50/24":           "diagnostic.address",
		"IPv4 gateway must be in the address subnet":                                "diagnostic.gateway",
		"networkd drop-ins require manual network setup":                            "diagnostic.network_complex",
		"separate networkd route/address sections require manual setup":             "diagnostic.network_complex",
		"multiple addresses require manual network setup":                           "diagnostic.network_complex",
		"DHCP plus static addressing requires manual setup":                         "diagnostic.network_complex",
		"missing OneKVM metadata; legacy/vendor images are rejected":                "diagnostic.metadata",
		"unsupported metadata version, lengths or flags":                            "diagnostic.version",
		"truncated image or invalid payload length":                                 "diagnostic.truncated",
		"image exceeds target capacity":                                             "diagnostic.capacity",
		"payload SHA-256 mismatch":                                                  "diagnostic.payload_hash",
		"trailer SHA-256 mismatch":                                                  "diagnostic.trailer_hash",
		"unsupported SD geometry (requires 128 MiB /boot)":                          "diagnostic.geometry",
		"run installation as root":                                                  "diagnostic.root",
		"another installer is running":                                              "diagnostic.lock",
		"recovery boot replacement has started; keep the staged installation files": "diagnostic.armed",
		"select an image outside the installer staging directory":                   "diagnostic.staged_source",
		"OneKVM is already installed; use the system updater":                       "diagnostic.installed",
		"installation profile is detected from hardware and cannot be overridden":   "diagnostic.profile",
	}
	if key, ok := keys[message]; ok {
		return t.T(key)
	}
	if strings.HasPrefix(message, "invalid DNS address ") {
		return t.T("diagnostic.dns", strings.TrimPrefix(message, "invalid DNS address "))
	}
	if value, ok := strings.CutPrefix(message, "wrong image type: expected "); ok {
		if expected, actual, found := strings.Cut(value, ", got "); found {
			return t.T("diagnostic.type", expected, actual)
		}
	}
	return message
}

// Operation maps the stable backend reporter labels to translated messages.
func (t Translator) Operation(label string) string {
	return t.T(OperationKey(label))
}

// OperationKey gives CLI labels and wizard labels the same step identity.
func OperationKey(label string) string {
	keys := map[string]string{
		"Downloading full image to userdata":                    "op.download",
		"Validating image metadata and SHA-256":                 "op.verify",
		"Extracting installer recovery kernel":                  "op.extract",
		"Installing recovery boot files (keep power connected)": "op.arm",
		"Rechecking staged image and configuration":             "op.recheck",
		"Loading complete image from userdata into RAM":         "op.load",
		"Validating complete RAM image":                         "op.ram_verify",
		"Writing SD image and verifying disk readback":          "op.write",
	}
	if key, ok := keys[label]; ok {
		return key
	}
	return label
}
