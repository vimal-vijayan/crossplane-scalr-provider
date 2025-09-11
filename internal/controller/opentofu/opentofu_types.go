package opentofu

import (
	"context"
)

type InitOptions struct {
	WorkingDir  string            `json:"workingDir,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	ExtraArgs   []string          `json:"extraArgs,omitempty"`
}

type PlanOptions struct {
	WorkingDir       string            `json:"workingDir,omitempty"`
	Variables        map[string]string `json:"variables,omitempty"`
	VarFiles         []string          `json:"varFiles,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	DetailedExitCode bool              `json:"detailedExitCode,omitempty"`
	Destroy          bool              `json:"destroy,omitempty"`
	ExtraArgs        []string          `json:"extraArgs,omitempty"`
}

type ApplyOptions struct {
	WorkingDir  string            `json:"workingDir,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	AutoApprove bool              `json:"autoApprove,omitempty"`
	PlanFile    string            `json:"planFile,omitempty"`
	ExtraArgs   []string          `json:"extraArgs,omitempty"`
}

type DestroyOptions struct {
	WorkingDir  string            `json:"workingDir,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	VarFiles    []string          `json:"varFiles,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	AutoApprove bool              `json:"autoApprove,omitempty"`
	ExtraArgs   []string          `json:"extraArgs,omitempty"`
}

type CloneOptions struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Directory  string `json:"directory"`
}

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    error  `json:"error,omitempty"`
}

type Runner interface {
	CloneRepository(ctx context.Context, opts CloneOptions) error
	Init(ctx context.Context, opts InitOptions) (*CommandResult, error)
	Plan(ctx context.Context, opts PlanOptions) (*CommandResult, error)
	Apply(ctx context.Context, opts ApplyOptions) (*CommandResult, error)
	Destroy(ctx context.Context, opts DestroyOptions) (*CommandResult, error)
	Cleanup(ctx context.Context, workingDir string) error
}

