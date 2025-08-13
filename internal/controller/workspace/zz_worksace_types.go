package workspace

type WorkspaceParameters struct {
	Organization    string              `json:"organization"`
	Name            string              `json:"name"`
	EnvironmentName string              `json:"environmentName"`
	Description     string              `json:"description"`
	Tags            []string            `json:"tags"`
	Attributes      workspaceAttributes `json:"workspaceParameters"`
}

type workspaceAttributes struct {
	ExecutionMode string `json:"executionMode"`
	IacPlatform   string `json:"iacPlatform"`
}
