package workspace

type Data struct {
	Attributes    Attributes    `json:"attributes"`
	Relationships Relationships `json:"relationships"`
	Type          string        `json:"type"`
}

type Attributes struct {
	AutoQueueRuns string `json:"auto-queue-runs"`
	ExecutionMode string `json:"execution-mode"`
	IacPlatform   string `json:"iac-platform"`
	Terragrunt    struct {
		IncludeExternalDependencies bool `json:"include-external-dependencies"`
		UseRunAll                   bool `json:"use-run-all"`
	} `json:"terragrunt"`
	VcsRepo struct {
		DryRunsEnabled    bool `json:"dry-runs-enabled"`
		IngressSubmodules bool `json:"ingress-submodules"`
	} `json:"vcs-repo"`
	AutoApply          bool   `json:"auto-apply"`
	Name               string `json:"name"`
	RemoteBackend      bool   `json:"remote-backend"`
	RemoteStateSharing bool   `json:"remote-state-sharing"`
}

type Relationships struct {
	AgentPool   struct{} `json:"agent-pool"`
	Environment struct{} `json:"environment"`
	Module      struct{} `json:"module-version"`
	Tags        struct{} `json:"tags"`
	VcsProvider struct{} `json:"vcs-provider"`
}
