package installer

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type solutionMachine struct {
	Machine
	match bool
	hooks []SolutionHook
}

func (m solutionMachine) Matches(string) bool           { return m.match }
func (m solutionMachine) SolutionHooks() []SolutionHook { return m.hooks }

func testIssue(id string, blocking bool) Issue {
	return Issue{ID: id, TitleKey: "title", DescriptionKey: "description", Blocking: blocking,
		Solutions: []Solution{{ID: "first", TitleKey: "first.title", BodyKey: "first.body"}, {ID: "second", TitleKey: "second.title", BodyKey: "second.body"}}}
}

func TestSolutionHooksRunAllAndKeepAllFindingsAfterFailure(t *testing.T) {
	var ran []string
	failed := errors.New("read failed")
	hook := func(id string, issues []Issue, err error) SolutionHook {
		return SolutionHook{ID: id, Check: func(_ context.Context, in CheckContext) (CheckResult, error) {
			if in.Root != "/fixture" || in.Phase != CheckBeforeInstall || in.Target.Profile != "fixture-sd" {
				t.Fatal("lost check context", in)
			}
			ran = append(ran, id)
			return CheckResult{Supported: true, Issues: issues}, err
		}}
	}
	wrong := solutionMachine{match: false, hooks: []SolutionHook{hook("wrong-machine", nil, nil)}}
	matched := solutionMachine{match: true, hooks: []SolutionHook{
		hook("first", []Issue{testIssue("first-issue", false)}, failed),
		hook("second", []Issue{testIssue("second-issue", false)}, nil),
		hook("third", []Issue{testIssue("third-issue", true)}, nil),
	}}
	result, err := checkSolutions(context.Background(), CheckContext{Root: "/fixture", Phase: CheckBeforeInstall, Target: Device{Profile: "fixture-sd"}}, []Machine{wrong, matched})
	if !errors.Is(err, failed) || !reflect.DeepEqual(ran, []string{"first", "second", "third"}) {
		t.Fatal("stopped at an earlier failure or ran another model's hook", ran, err)
	}
	if !result.Supported || !result.Blocking() || len(result.Checks) != 3 || len(result.Issues) != 3 || len(result.Issues[1].Solutions) != 2 {
		t.Fatal("did not retain the complete result", result)
	}
	if result.Checks[0].Error == "" || result.Checks[1].Error != "" || result.Checks[2].Error != "" {
		t.Fatal("misreported individual outcomes", result.Checks)
	}
}

func TestInvalidIssueCannotTurnFailedCheckIntoSuccess(t *testing.T) {
	runs := 0
	m := solutionMachine{match: true, hooks: []SolutionHook{
		{ID: "bad", Check: func(context.Context, CheckContext) (CheckResult, error) {
			return CheckResult{Supported: true, Issues: []Issue{{ID: "missing-guidance"}}}, nil
		}},
		{ID: "next", Check: func(context.Context, CheckContext) (CheckResult, error) {
			runs++
			return CheckResult{Supported: true}, nil
		}},
	}}
	result, err := checkSolutions(context.Background(), CheckContext{}, []Machine{m})
	if err == nil || !result.Blocking() || runs != 1 || len(result.Checks) != 2 {
		t.Fatal("invalid result was ignored or prevented the remaining checks", result, err)
	}
}

func TestSolutionsCancellationAndNoMatchingMachine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkSolutions(ctx, CheckContext{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	result, err := checkSolutions(context.Background(), CheckContext{}, []Machine{solutionMachine{match: false}})
	if err != nil || result.Supported || result.Blocking() || len(result.Issues) != 0 {
		t.Fatal("unsupported machine was treated as a checked machine", result, err)
	}
}
