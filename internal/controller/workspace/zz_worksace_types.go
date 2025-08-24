package workspace

type Data struct {
	Attributes    Attributes    `json:"attributes"`
	Relationships Relationships `json:"relationships"`
	Type          string        `json:"type"`
}

type Attributes struct {
	AutoQueueRuns      string `json:"auto-queue-runs"`
	ExecutionMode      string `json:"execution-mode"`
	IacPlatform        string `json:"iac-platform"`
	AutoApply          bool   `json:"auto-apply"`
	Name               string `json:"name"`
	RemoteBackend      bool   `json:"remote-backend"`
	RemoteStateSharing bool   `json:"remote-state-sharing"`
}

type Relationships struct {
	Environment Environment `json:"environment"`
	Tags        []string    `json:"tags"`
}

type Environment struct {
	Data struct {
		Id string `json:"id"`
	} `json:"data"`
}
