package i18n

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

func TestLocalePrecedenceAndExplicitLanguage(t *testing.T) {
	for _, tt := range []struct {
		arg, all, messages, lang, want string
	}{
		{"auto", "", "", "zh_CN.UTF-8", "zh"},
		{"auto", "C", "zh_CN.UTF-8", "zh_CN.UTF-8", "en"},
		{"auto", "", "en_US.UTF-8", "zh_CN.UTF-8", "en"},
		{"en", "zh_CN.UTF-8", "", "", "en"},
		{"zh-CN", "C", "", "", "zh"},
		{"zh-TW", "C", "", "", "zh-TW"},
		{"ZH_tw", "zh_CN.UTF-8", "", "", "zh-TW"},
		{"zh-HK", "C", "", "", "zh-TW"},
		{"zh-MO", "C", "", "", "zh-TW"},
		{"zh-Hant", "C", "", "", "zh-TW"},
		{"zh-Hant-TW", "C", "", "", "zh-TW"},
		{"zh-Hant-HK", "C", "", "", "zh-TW"},
		{"zh-Hans", "C", "", "", "zh"},
		{"auto", "", "", "zh_TW.UTF-8", "zh-TW"},
		{"auto", "", "", "zh_HK.UTF-8", "zh-TW"},
		{"auto", "", "", "zh_MO.UTF-8", "zh-TW"},
		{"auto", "zh_TW.UTF-8", "en_US.UTF-8", "zh_CN.UTF-8", "zh-TW"},
		{"auto", "C", "zh_TW.UTF-8", "zh_TW.UTF-8", "en"},
		{"auto", "", "zh_Hant.UTF-8", "en_US.UTF-8", "zh-TW"},
		{"auto", "", "", "zh_SG.UTF-8", "zh"},
		{"auto", "", "", "de_DE.UTF-8", "en"},
	} {
		env := map[string]string{"LC_ALL": tt.all, "LC_MESSAGES": tt.messages, "LANG": tt.lang}
		tr, err := New(tt.arg, func(k string) string { return env[k] })
		if err != nil || tr.Language != tt.want {
			t.Fatalf("%+v: got %s, %v", tt, tr.Language, err)
		}
	}
	if _, err := New("typo", func(string) string { return "" }); err == nil {
		t.Fatal("explicit unsupported language accepted")
	}
}

func TestCatalogKeysAndFormatArgumentsMatch(t *testing.T) {
	var en map[string]string
	b, err := files.ReadFile("locales/en.json")
	if err != nil || json.Unmarshal(b, &en) != nil {
		t.Fatal("invalid English catalog", err)
	}
	format := regexp.MustCompile(`%[-+ #0-9.]*[a-zA-Z%]`)
	for _, language := range []string{"zh", "zh-TW"} {
		t.Run(language, func(t *testing.T) {
			var catalog map[string]string
			b, err := files.ReadFile("locales/" + language + ".json")
			if err != nil || json.Unmarshal(b, &catalog) != nil {
				t.Fatal("invalid catalog", err)
			}
			if len(en) != len(catalog) {
				t.Fatal("catalog sizes differ")
			}
			for key, value := range en {
				if catalog[key] == "" || !reflect.DeepEqual(format.FindAllString(value, -1), format.FindAllString(catalog[key], -1)) {
					t.Errorf("missing translation or incompatible format for %s", key)
				}
			}
		})
	}
}
