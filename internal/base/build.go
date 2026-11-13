// Package base builds a base image: a throwaway VM, provisioned step by step, saved as a template.
package base

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PillowPillow/den/internal/config"
	"github.com/PillowPillow/den/internal/instance"
	"github.com/PillowPillow/den/internal/sbx"
	"github.com/PillowPillow/den/internal/sbxenv"
)

// Build runs b's provisioning in a build VM named <name>-build and saves it as b.Image.
func Build(ctx context.Context, d instance.Deps, name string, b config.Base) (err error) {
	if err := instance.CheckSbx(ctx, d.Sbx); err != nil {
		return err
	}
	prov, err := ReadProvisioning(b.Build.Includes, b.Build.Steps)
	if err != nil {
		return err
	}
	vm := name + "-build"
	if err := sbx.ValidateCreatableSandboxName(vm); err != nil {
		return err
	}
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return err
	}
	// The deferred env rm below would remove a VM den did not create.
	if sbx.Find(boxes, vm) != nil {
		return fmt.Errorf("a sandbox named %s already exists, and den would remove it when the build ends — "+
			"run `sbx rm %s` if it is a leftover, then `den build %s` again", vm, vm, name)
	}

	scratch := filepath.Join(d.DenHome, "build", name)
	if err := os.RemoveAll(scratch); err != nil {
		return fmt.Errorf("clearing %s: %w", scratch, err)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", scratch, err)
	}
	// The VM writes into scratch, so the files sbx reads on the host live beside it, not in it.
	gen := filepath.Join(d.DenHome, "gen", ".build", name)
	if err := os.RemoveAll(gen); err != nil {
		return fmt.Errorf("clearing %s: %w", gen, err)
	}
	if err := instance.WriteGenFile(filepath.Join(gen, "network", "spec.yaml"), sbxenv.NetworkKit(b.Build.Network.Allow)); err != nil {
		return err
	}
	claudeKit, err := instance.WriteDenClaudeKit(d.DenHome)
	if err != nil {
		return err
	}
	env, err := sbxenv.Render(sbxenv.Env{
		Name:      vm,
		Kits:      []string{claudeKit, filepath.Join(gen, "network")},
		Workspace: scratch,
		Template:  b.From,
	})
	if err != nil {
		return err
	}
	envFile := filepath.Join(gen, instance.EnvFileName)
	if err := instance.WriteGenFile(envFile, env); err != nil {
		return err
	}

	if err := d.Sbx.Stream(ctx, d.Err, "--no-input", "env", "create", "--force", "--skip-host-commands", envFile); err != nil {
		err = fmt.Errorf("creating the build VM %s: %w", vm, err)
		return removeVM(ctx, d, vm, envFile, err)
	}
	return removeVM(ctx, d, vm, envFile, provision(ctx, d, vm, b.Image, prov))
}

func provision(ctx context.Context, d instance.Deps, vm, image string, prov Provisioning) error {
	for i, s := range prov.Steps {
		if err := d.Sbx.Stream(ctx, d.Out, "exec", vm, "--", "bash", "-lc", prov.Payload(i)); err != nil {
			return fmt.Errorf("step %d/%d %s: %w", i+1, len(prov.Steps), s.Path, err)
		}
	}
	if err := d.Sbx.Stream(ctx, d.Err, "stop", vm); err != nil {
		return fmt.Errorf("stopping %s: %w", vm, err)
	}
	if err := d.Sbx.Stream(ctx, d.Err, "template", "save", vm, image); err != nil {
		return fmt.Errorf("saving %s as %s: %w", vm, image, err)
	}
	return nil
}

// removeVM runs even when the context is cancelled, and never masks cause with its own failure.
func removeVM(ctx context.Context, d instance.Deps, vm, envFile string, cause error) error {
	err := d.Sbx.Stream(context.WithoutCancel(ctx), d.Err, "--no-input", "env", "rm", "--force", "--skip-host-commands", envFile)
	if err != nil {
		fmt.Fprintf(d.Err, "warning: removing the build VM %s: %v — run `sbx rm %s`\n", vm, err, vm)
	}
	return cause
}
