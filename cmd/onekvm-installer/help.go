package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/onekvm/onekvm-installer/internal/buildinfo"
	"github.com/onekvm/onekvm-installer/internal/i18n"
)

type commandHelp struct {
	name, usage string
	options     []string
	examples    []string
}

var commands = []commandHelp{
	{"install", "[options]", []string{"image", "url", "interface", "tag", "catalog"}, []string{
		"onekvm-installer", "onekvm-installer install --image /data/OneKVM.img", "onekvm-installer install --url https://mirror.example/onekvm.img",
	}},
	{"prepare", "[options]", []string{"image", "url", "interface", "tag", "catalog"}, []string{
		"onekvm-installer prepare --image /data/OneKVM.img", "onekvm-installer prepare --url https://mirror.example/onekvm.img", "onekvm-installer arm",
	}},
	{"arm", "[options]", nil, []string{"onekvm-installer arm"}},
	{"inspect", "[options]", nil, []string{"onekvm-installer inspect"}},
	{"network", "[options]", []string{"interface"}, []string{"onekvm-installer network --interface eth0"}},
	{"releases", "[options]", []string{"tag", "catalog", "profile"}, []string{
		"onekvm-installer releases", "onekvm-installer releases --profile nanokvm",
	}},
	{"verify", "--image PATH [options]", []string{"image", "profile"}, []string{
		"onekvm-installer verify --image /data/OneKVM.img --profile nanokvm",
	}},
	{"solution", "[options]", []string{"image", "json", "repair"}, []string{
		"onekvm-installer solution --image /data/OneKVM.img", "onekvm-installer solution --image /data/OneKVM.img --json",
	}},
}

var optionSyntax = map[string]string{
	"image":     "--image PATH",
	"url":       "--url URL, --mirror URL",
	"interface": "--interface NAME",
	"tag":       "--tag TAG",
	"catalog":   "--catalog PATH",
	"profile":   "--profile TYPE",
	"lang":      "--lang auto|en|zh|zh-TW",
	"version":   "--version",
	"help":      "-h, --help",
	"json":      "--json",
	"repair":    "--repair ID",
}

func writeHelp(w io.Writer, tr i18n.Translator, command string) error {
	t := tr.T
	var selected *commandHelp
	if command != "" {
		for i := range commands {
			if commands[i].name == command {
				selected = &commands[i]
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("unknown command %q", command)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s v%s\n\n", t("app.title"), buildinfo.Version)
	section := func(key string) { fmt.Fprintf(&out, "\n%s\n", t("cli.help."+key)) }
	row := func(label, description string) { fmt.Fprintf(&out, "  %-22s %s\n", label, description) }
	var options, examples []string
	if selected == nil {
		fmt.Fprintln(&out, t("cli.help.intro"))
		section("usage")
		fmt.Fprintln(&out, "  onekvm-installer [command] [options]")
		section("install")
		for _, cmd := range commands[:3] {
			row(cmd.name, t("cli.command."+cmd.name))
		}
		section("inspect")
		for _, cmd := range commands[3:] {
			row(cmd.name, t("cli.command."+cmd.name))
		}
		options = []string{"image", "url", "interface", "tag", "catalog", "profile"}
		examples = []string{
			"onekvm-installer", "onekvm-installer --image /data/OneKVM.img", "onekvm-installer arm",
		}
	} else {
		fmt.Fprintln(&out, t("cli.command."+selected.name))
		section("usage")
		fmt.Fprintf(&out, "  onekvm-installer %s %s\n", selected.name, selected.usage)
		options, examples = selected.options, selected.examples
	}
	section("options")
	for _, option := range append(append([]string{}, options...), "lang", "version", "help") {
		row(optionSyntax[option], t("flag."+option))
	}
	if command == "verify" || command == "releases" {
		fmt.Fprintf(&out, "\n  %s\n", t("cli.help.profiles"))
	}
	section("examples")
	for _, example := range examples {
		fmt.Fprintln(&out, "  "+example)
	}
	if command == "" {
		fmt.Fprintf(&out, "\n%s\n", t("cli.help.more"))
	} else if command == "install" || command == "prepare" || command == "arm" {
		fmt.Fprintf(&out, "\n%s\n", t("cli.help."+command+".note"))
	}
	_, err := io.WriteString(w, out.String())
	return err
}
