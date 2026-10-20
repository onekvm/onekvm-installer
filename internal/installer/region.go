package installer

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // The factory system need not have an IANA database installed.
)

type SystemSettings struct {
	Language string
	Timezone string
}

func (s SystemSettings) Normalize() (SystemSettings, error) {
	switch s.Language {
	case "", "en":
		s.Language = "en"
	case "zh", "zh-CN", "zh_cn":
		s.Language = "zh"
	case "zh-TW", "zh_tw":
		s.Language = "zh_tw"
	default:
		return s, fmt.Errorf("unsupported device language")
	}
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	if s.Timezone == "Local" || strings.ContainsAny(s.Timezone, "\\\x00\r\n\t '") {
		return s, fmt.Errorf("enter an IANA time zone, such as UTC or Asia/Shanghai")
	}
	for _, part := range strings.Split(s.Timezone, "/") {
		if part == "" || part == "." || part == ".." {
			return s, fmt.Errorf("invalid IANA time zone")
		}
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return s, fmt.Errorf("unknown IANA time zone %q", s.Timezone)
	}
	return s, nil
}
