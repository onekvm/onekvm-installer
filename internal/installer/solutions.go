package installer

import (
	"context"
	"errors"
	"fmt"
)

type CheckPhase string

const (
	CheckImageReady    CheckPhase = "image-ready"
	CheckBeforeInstall CheckPhase = "before-install"
)

// CheckContext is deliberately separate from Probe: troubleshooting must also
// work when the current storage layout cannot be used for installation.
type CheckContext struct {
	Root      string
	Phase     CheckPhase
	Target    Device
	Image     Image
	ImagePath string
}

// Issue describes a detected condition and its guidance or registered repairs. Message keys
// belong to the embedded catalogs; the machine hook owns applicability and
// whether continuing would be unsafe or ineffective.
type Issue struct {
	ID             string     `json:"id"`
	TitleKey       string     `json:"title_key"`
	DescriptionKey string     `json:"description_key"`
	Solutions      []Solution `json:"solutions"`
	Blocking       bool       `json:"blocking"`
}

type Solution struct {
	ID       string `json:"id"`
	TitleKey string `json:"title_key"`
	BodyKey  string `json:"body_key"`
	RepairID string `json:"repair_id,omitempty"`
}

type CheckOutcome struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

type CheckResult struct {
	Supported bool           `json:"supported"`
	Issues    []Issue        `json:"issues"`
	Checks    []CheckOutcome `json:"checks"`
}

func (r CheckResult) Blocking() bool {
	for _, check := range r.Checks {
		if check.Error != "" {
			return true
		}
	}
	for _, issue := range r.Issues {
		if issue.Blocking {
			return true
		}
	}
	return false
}

// SolutionHook is a read-only check: no mounting,
// firmware writes, boot-environment changes, or rebooting. Repairs are separate
// registered actions and require their own review and explicit confirmation.
type SolutionHook struct {
	ID    string
	Check func(context.Context, CheckContext) (CheckResult, error)
}

// A machine may provide several hooks. The common workflow runs every hook in
// order and aggregates findings, including when an earlier check failed.
type SolutionProvider interface {
	SolutionHooks() []SolutionHook
}

type RepairFact struct {
	LabelKey string `json:"label_key"`
	Value    string `json:"value"`
}

type RepairPlan struct {
	ID         string                                `json:"id"`
	TitleKey   string                                `json:"title_key"`
	SummaryKey string                                `json:"summary_key"`
	Facts      []RepairFact                          `json:"facts"`
	Apply      func(context.Context, Reporter) error `json:"-"`
}

type RepairHook struct {
	ID      string
	Prepare func(context.Context, CheckContext) (RepairPlan, error)
}

type RepairProvider interface{ SolutionRepairs() []RepairHook }

// Preparing a repair is read-only. Only actions attached to a currently
// detected issue can be prepared; arbitrary scripts or action IDs cannot run.
func PrepareRepair(ctx context.Context, in CheckContext, id string) (RepairPlan, error) {
	result, err := CheckSolutions(ctx, in)
	if err != nil {
		return RepairPlan{}, err
	}
	allowed := false
	for _, issue := range result.Issues {
		for _, solution := range issue.Solutions {
			allowed = allowed || (id != "" && solution.RepairID == id)
		}
	}
	if !allowed {
		return RepairPlan{}, fmt.Errorf("repair is not available for a current device issue")
	}
	for _, machine := range Machines() {
		if !machine.Matches(in.Root) {
			continue
		}
		if provider, ok := machine.(RepairProvider); ok {
			for _, hook := range provider.SolutionRepairs() {
				if hook.ID == id && hook.Prepare != nil {
					plan, err := hook.Prepare(ctx, in)
					if err != nil {
						return RepairPlan{}, err
					}
					if plan.ID != id || plan.Apply == nil || plan.TitleKey == "" || plan.SummaryKey == "" {
						return RepairPlan{}, fmt.Errorf("invalid repair plan")
					}
					return plan, nil
				}
			}
		}
		break
	}
	return RepairPlan{}, fmt.Errorf("this installer does not provide the requested repair")
}

func CheckSolutions(ctx context.Context, in CheckContext) (CheckResult, error) {
	if in.Image.Profile == "" || in.ImagePath == "" {
		return CheckResult{}, fmt.Errorf("select an image before checking solutions")
	}
	selected, err := MachineFor(in.Image.Profile)
	if err != nil {
		return CheckResult{}, err
	}
	for _, machine := range Machines() {
		if machine.Matches(in.Root) && machine.Family() != selected.Family() {
			return CheckResult{}, fmt.Errorf("wrong image family: %s", in.Image.Profile)
		}
	}
	return checkSolutions(ctx, in, Machines())
}

func checkSolutions(ctx context.Context, in CheckContext, machines []Machine) (CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	for _, machine := range machines {
		if !machine.Matches(in.Root) {
			continue
		}
		provider, ok := machine.(SolutionProvider)
		if !ok {
			return CheckResult{}, nil
		}
		result := CheckResult{Issues: []Issue{}, Checks: []CheckOutcome{}}
		seen, hooks := make(map[string]bool), make(map[string]bool)
		var failures []error
		for _, hook := range provider.SolutionHooks() {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			var current CheckResult
			var err error
			if hook.ID == "" || hooks[hook.ID] || hook.Check == nil {
				err = fmt.Errorf("invalid solution hook: %s", hook.ID)
			} else {
				hooks[hook.ID] = true
				current, err = hook.Check(ctx, in)
			}
			result.Supported = result.Supported || current.Supported
			for _, issue := range current.Issues {
				if !current.Supported || seen[issue.ID] || !validIssue(issue) {
					err = errors.Join(err, fmt.Errorf("invalid solution hook result: %s", hook.ID))
					continue
				}
				seen[issue.ID] = true
				result.Issues = append(result.Issues, issue)
			}
			outcome := CheckOutcome{ID: hook.ID}
			if err != nil {
				outcome.Error = err.Error()
				failures = append(failures, fmt.Errorf("%s: %w", hook.ID, err))
			}
			result.Checks = append(result.Checks, outcome)
		}
		return result, errors.Join(append(failures, ctx.Err())...)
	}
	return CheckResult{}, nil
}

func validIssue(issue Issue) bool {
	if issue.ID == "" || issue.TitleKey == "" || issue.DescriptionKey == "" || len(issue.Solutions) == 0 {
		return false
	}
	seen := make(map[string]bool)
	for _, solution := range issue.Solutions {
		if solution.ID == "" || seen[solution.ID] || solution.TitleKey == "" || solution.BodyKey == "" {
			return false
		}
		seen[solution.ID] = true
	}
	return true
}
