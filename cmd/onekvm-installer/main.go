package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onekvm/onekvm-installer/internal/buildinfo"
	"github.com/onekvm/onekvm-installer/internal/i18n"
	"github.com/onekvm/onekvm-installer/internal/installer"
	_ "github.com/onekvm/onekvm-installer/internal/machines"
	"github.com/onekvm/onekvm-installer/internal/ui"
	"golang.org/x/term"
)

func jsonOut(v any) error { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); return e.Encode(v) }

// Recovery and machine-readable commands log to stderr without opening a TUI.
func consoleReporter(tr i18n.Translator) installer.Reporter {
	return func(label string, op func(installer.Progress) error) error {
		label = tr.Operation(label)
		fmt.Fprintln(os.Stderr, label)
		var mu sync.Mutex
		last := time.Now()
		err := op(func(done, total int64) {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(last) < 5*time.Second {
				return
			}
			last = time.Now()
			if total > 0 {
				fmt.Fprintf(os.Stderr, "%s: %.0f%% (%.1f MiB)\n", label, float64(done)*100/float64(total), float64(done)/installer.MiB)
			} else {
				fmt.Fprintf(os.Stderr, "%s: %.1f MiB\n", label, float64(done)/installer.MiB)
			}
		})
		if err == nil {
			fmt.Fprintln(os.Stderr, "✓ "+label)
		}
		return err
	}
}

func languageFromArgs(args []string) string {
	lang := "auto"
	for i, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		option := strings.TrimLeft(arg, "-")
		if value, ok := strings.CutPrefix(option, "lang="); ok {
			lang = value
		} else if option == "lang" && i+1 < len(args) {
			lang = args[i+1]
		}
	}
	return lang
}

func execute(ctx context.Context, args []string) error {
	tr, err := i18n.New(languageFromArgs(args), os.Getenv)
	if err != nil {
		return err
	}
	name := "install"
	helpTarget := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
		helpTarget = name
	}
	if name == "help" {
		helpTarget = ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			helpTarget, args = args[0], args[1:]
		}
	}
	if name == "version" {
		fmt.Println("onekvm-installer " + buildinfo.Version)
		return nil
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { _ = writeHelp(fs.Output(), tr, helpTarget) }
	image := fs.String("image", "", tr.T("flag.image"))
	tag := fs.String("tag", "", tr.T("flag.tag"))
	catalogPath := fs.String("catalog", "", tr.T("flag.catalog"))
	iface := fs.String("interface", "eth0", tr.T("flag.interface"))
	profile := fs.String("profile", "", tr.T("flag.profile"))
	fs.String("lang", "auto", tr.T("flag.lang"))
	showVersion := fs.Bool("version", false, tr.T("flag.version"))
	jsonOutput := fs.Bool("json", false, tr.T("flag.json"))
	repairID := fs.String("repair", "", tr.T("flag.repair"))
	showHelp := fs.Bool("help", false, tr.T("flag.help"))
	fs.BoolVar(showHelp, "h", false, tr.T("flag.help"))
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if name == "help" || *showHelp {
		return writeHelp(os.Stdout, tr, helpTarget)
	}
	if *showVersion {
		fmt.Println("onekvm-installer " + buildinfo.Version)
		return nil
	}
	if *jsonOutput && name != "solution" {
		return fmt.Errorf("--json is only supported by solution")
	}
	if name == "solution" {
		if *profile != "" {
			return fmt.Errorf("installation profile is detected from hardware and cannot be overridden")
		}
		in := installer.CheckContext{Root: "/", Phase: installer.CheckImageReady}
		plain := *jsonOutput || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))
		if plain || *repairID != "" {
			if *image == "" {
				return fmt.Errorf("%s", tr.T("diagnostic.solution_image"))
			}
			info, err := installer.VerifyContext(ctx, *image, "", 4*1024*installer.MiB, nil)
			if err != nil {
				return err
			}
			in.Image, in.ImagePath = info, *image
			in.Target.Profile = info.Profile
		}
		if *repairID != "" {
			plan, err := installer.PrepareRepair(ctx, in, *repairID)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return jsonOut(plan)
			}
			fmt.Println(tr.T(plan.TitleKey) + "\n" + tr.T(plan.SummaryKey))
			for _, fact := range plan.Facts {
				fmt.Printf("%s: %s\n", tr.T(fact.LabelKey), fact.Value)
			}
			return nil
		}
		// Read-only image checks do not need the installation storage layout.
		if plain {
			result, err := installer.CheckSolutions(ctx, in)
			if *jsonOutput {
				if writeErr := jsonOut(result); writeErr != nil {
					return writeErr
				}
			} else {
				fmt.Print(ui.WriteSolutions(result, tr.T))
			}
			if err == nil && result.Blocking() {
				return fmt.Errorf("%s", tr.T("solutions.blocked"))
			}
			return err
		}
		unlock, err := installer.Lock(installer.LockPath)
		if err != nil {
			return err
		}
		defer unlock()
		return ui.Run(ctx, ui.Options{Command: name, Image: *image, Text: tr})
	}
	if *repairID != "" {
		return fmt.Errorf("--repair is only supported by solution")
	}
	unlock, err := installer.Lock(installer.LockPath)
	if err != nil {
		return err
	}
	defer unlock()
	report := consoleReporter(tr)
	if name == "recover" {
		if len(args) != 0 {
			return fmt.Errorf("recover takes no arguments")
		}
		installed, err := installer.Recover(report)
		if err != nil {
			return err
		}
		if installed {
			fmt.Println(tr.T("cli.recovered"))
			syscall.Sync()
			return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
		}
		return nil
	}
	if *profile != "" && name != "verify" && name != "releases" {
		return fmt.Errorf("installation profile is detected from hardware and cannot be overridden")
	}
	if name == "network" {
		n, err := installer.CopyNetwork("/", *iface)
		if err != nil {
			return err
		}
		return jsonOut(n)
	}
	if name == "verify" {
		if *image == "" {
			return fmt.Errorf("verify requires --image PATH")
		}
		var info installer.Image
		err := report("Validating image metadata and SHA-256", func(p installer.Progress) error {
			var e error
			info, e = installer.Verify(*image, *profile, 4*1024*installer.MiB, p)
			return e
		})
		if err != nil {
			return err
		}
		return jsonOut(info)
	}
	if name != "inspect" && name != "releases" && name != "prepare" && name != "install" && name != "arm" {
		return fmt.Errorf("unknown command %q", name)
	}
	if name == "install" || name == "prepare" || name == "arm" {
		if err := installer.CheckInstallTarget("/"); err != nil {
			return err
		}
	}
	catalog, err := installer.LoadCatalog(*catalogPath)
	if err != nil {
		return err
	}
	var d installer.Device
	if name != "releases" || *profile == "" {
		d, err = installer.Probe("/")
		if err != nil {
			return err
		}
	} else {
		d.Profile = *profile
	}
	if name == "inspect" {
		return jsonOut(d)
	}
	if name == "releases" {
		repo := catalog[d.Profile]
		if repo == "" {
			return fmt.Errorf("no repository for %s", d.Profile)
		}
		rel, err := installer.FetchRelease(ctx, repo, *tag)
		if err != nil {
			return err
		}
		assets := installer.ImageAssets(rel, d.Profile)
		if *profile == "" {
			assets = installer.CandidateImages(rel, d.Profile)
		}
		return jsonOut(struct {
			Repository string                   `json:"repository"`
			Tag        string                   `json:"tag"`
			Images     []installer.ReleaseAsset `json:"images"`
		}{repo, rel.Tag, assets})
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("run installation as root")
	}
	if err = d.CanArm(); err != nil {
		return err
	}
	if err = installer.CheckRuntime(d); err != nil {
		return err
	}
	if name == "install" || name == "prepare" {
		if _, err := os.Stat(filepath.Join("/sys/class/net", *iface)); err != nil {
			return fmt.Errorf("%s", tr.T("error.interface", *iface))
		}
	}
	return ui.Run(ctx, ui.Options{Command: name, Image: *image, Tag: *tag, Interface: *iface, Device: d, Catalog: catalog, Text: tr})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		tr, trErr := i18n.New(languageFromArgs(os.Args[1:]), os.Getenv)
		if trErr != nil {
			tr, _ = i18n.New("en", os.Getenv)
		}
		fmt.Fprintln(os.Stderr, tr.T("cli.error"), tr.Diagnostic(err.Error()))
		os.Exit(1)
	}
}
