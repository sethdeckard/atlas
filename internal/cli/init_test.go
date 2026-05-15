package cli_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/sethdeckard/atlas/internal/cli"
	"github.com/sethdeckard/atlas/internal/config"
)

func TestInit_InvokesOnboardingSeamAndPropagatesError(t *testing.T) {
	defer cleanupCacheEnv(t)

	want := errors.New("simulated prompt failure")
	called := 0
	restore := cli.SetRunInitForTest(func(_ context.Context, _ string, _ config.Config) error {
		called++
		return want
	})
	defer restore()

	cmd := cli.NewInitCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())

	err := cmd.RunE(cmd, nil)
	if !errors.Is(err, want) {
		t.Errorf("expected %v; got %v", want, err)
	}
	if called != 1 {
		t.Errorf("expected onboarding to be invoked once; got %d", called)
	}
}

func TestInit_SuccessReturnsNil(t *testing.T) {
	defer cleanupCacheEnv(t)

	restore := cli.SetRunInitForTest(func(_ context.Context, _ string, _ config.Config) error {
		return nil
	})
	defer restore()

	cmd := cli.NewInitCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("expected nil error; got %v", err)
	}
}
