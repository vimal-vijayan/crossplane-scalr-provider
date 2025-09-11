package opentofu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var log = logging.NewLogrLogger(zap.New(zap.UseDevMode(true)))

// DefaultRunner implements the Runner interface
type DefaultRunner struct {
	logger logging.Logger
}

// NewDefaultRunner creates a new DefaultRunner
func NewDefaultRunner() Runner {
	return &DefaultRunner{
		logger: log,
	}
}

// CloneRepository clones a git repository to the specified directory
func (r *DefaultRunner) CloneRepository(ctx context.Context, opts CloneOptions) error {
	r.logger.Info("Cloning repository", "repo", opts.Repository, "dir", opts.Directory)
	
	// Ensure the parent directory exists
	if err := os.MkdirAll(filepath.Dir(opts.Directory), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Remove existing directory if it exists
	if err := os.RemoveAll(opts.Directory); err != nil {
		return fmt.Errorf("failed to remove existing directory: %w", err)
	}

	args := []string{"clone", opts.Repository, opts.Directory}
	
	// Add branch or tag if specified
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	} else if opts.Tag != "" {
		args = append(args, "--branch", opts.Tag)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to clone repository: %w, output: %s", err, string(output))
	}

	r.logger.Info("Repository cloned successfully", "dir", opts.Directory)
	return nil
}

// Init runs tofu init
func (r *DefaultRunner) Init(ctx context.Context, opts InitOptions) (*CommandResult, error) {
	r.logger.Info("Running tofu init", "workingDir", opts.WorkingDir)
	
	args := []string{"init"}
	args = append(args, opts.ExtraArgs...)
	
	return r.runCommand(ctx, opts.WorkingDir, opts.Environment, args...)
}

// Plan runs tofu plan
func (r *DefaultRunner) Plan(ctx context.Context, opts PlanOptions) (*CommandResult, error) {
	r.logger.Info("Running tofu plan", "workingDir", opts.WorkingDir, "destroy", opts.Destroy)
	
	// Always run init before plan
	initResult, err := r.Init(ctx, InitOptions{
		WorkingDir:  opts.WorkingDir,
		Environment: opts.Environment,
	})
	if err != nil || initResult.ExitCode != 0 {
		return nil, fmt.Errorf("failed to initialize before plan: %w, output: %s", err, initResult.Stderr)
	}
	
	args := []string{"plan"}
	
	if opts.Destroy {
		args = append(args, "-destroy")
	}
	
	if opts.DetailedExitCode {
		args = append(args, "-detailed-exitcode")
	} else {
		args = append(args, "-out=tfplan")
	}
	
	// Add variables
	for key, value := range opts.Variables {
		args = append(args, "-var", fmt.Sprintf("%s=%s", key, value))
	}
	
	// Add var files
	for _, varFile := range opts.VarFiles {
		args = append(args, "-var-file", varFile)
	}
	
	args = append(args, opts.ExtraArgs...)
	
	return r.runCommand(ctx, opts.WorkingDir, opts.Environment, args...)
}

// Apply runs tofu apply
func (r *DefaultRunner) Apply(ctx context.Context, opts ApplyOptions) (*CommandResult, error) {
	r.logger.Info("Running tofu apply", "workingDir", opts.WorkingDir, "autoApprove", opts.AutoApprove)
	
	// Always run init before apply
	initResult, err := r.Init(ctx, InitOptions{
		WorkingDir:  opts.WorkingDir,
		Environment: opts.Environment,
	})
	if err != nil || initResult.ExitCode != 0 {
		return nil, fmt.Errorf("failed to initialize before apply: %w, output: %s", err, initResult.Stderr)
	}
	
	args := []string{"apply"}
	
	if opts.AutoApprove {
		args = append(args, "-auto-approve")
	}
	
	if opts.PlanFile != "" {
		args = append(args, opts.PlanFile)
	} else {
		args = append(args, "-input=false")
		if !opts.AutoApprove {
			args = append(args, "tfplan")
		}
	}
	
	args = append(args, opts.ExtraArgs...)
	
	return r.runCommand(ctx, opts.WorkingDir, opts.Environment, args...)
}

// Destroy runs tofu destroy
func (r *DefaultRunner) Destroy(ctx context.Context, opts DestroyOptions) (*CommandResult, error) {
	r.logger.Info("Running tofu destroy", "workingDir", opts.WorkingDir, "autoApprove", opts.AutoApprove)
	
	// Always run init before destroy
	initResult, err := r.Init(ctx, InitOptions{
		WorkingDir:  opts.WorkingDir,
		Environment: opts.Environment,
	})
	if err != nil || initResult.ExitCode != 0 {
		return nil, fmt.Errorf("failed to initialize before destroy: %w, output: %s", err, initResult.Stderr)
	}
	
	args := []string{"destroy"}
	
	if opts.AutoApprove {
		args = append(args, "-auto-approve")
	}
	
	// Add variables
	for key, value := range opts.Variables {
		args = append(args, "-var", fmt.Sprintf("%s=%s", key, value))
	}
	
	// Add var files
	for _, varFile := range opts.VarFiles {
		args = append(args, "-var-file", varFile)
	}
	
	args = append(args, opts.ExtraArgs...)
	
	return r.runCommand(ctx, opts.WorkingDir, opts.Environment, args...)
}

// Cleanup removes the working directory
func (r *DefaultRunner) Cleanup(ctx context.Context, workingDir string) error {
	r.logger.Info("Cleaning up working directory", "dir", workingDir)
	return os.RemoveAll(workingDir)
}

// runCommand executes a tofu command and returns the result
func (r *DefaultRunner) runCommand(ctx context.Context, workingDir string, environment map[string]string, args ...string) (*CommandResult, error) {
	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = workingDir
	
	// Set base environment variables
	cmd.Env = os.Environ()
	
	// Add custom environment variables
	for key, value := range environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}
	
	r.logger.Debug("Executing command", "cmd", "tofu", "args", strings.Join(args, " "), "workingDir", workingDir, "env", environment)
	
	output, err := cmd.CombinedOutput()
	exitCode := 0
	
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
	}
	
	result := &CommandResult{
		ExitCode: exitCode,
		Stdout:   string(output),
		Stderr:   string(output), // Combined output for simplicity
		Error:    err,
	}
	
	r.logger.Debug("Command completed", "exitCode", exitCode, "output", string(output))
	
	return result, nil
}

// Legacy functions for backward compatibility
func InitCommand(opts InitOptions) []string {
	cmd := []string{"init"}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}

func PlanCommand(opts PlanOptions) []string {
	cmd := []string{"plan"}
	if opts.DetailedExitCode {
		cmd = append(cmd, "-detailed-exitcode")
	} else {
		cmd = append(cmd, "-out=tfplan")
	}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}

func ApplyCommand(opts ApplyOptions) []string {
	cmd := []string{"apply"}
	if opts.AutoApprove {
		cmd = append(cmd, "-auto-approve")
	} else {
		cmd = append(cmd, "-input=false", "tfplan")
	}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}

