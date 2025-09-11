package opentofu


type InitOptions struct {
	ExtraArgs []string `json:"extraArgs,omitempty"`
}


type PlanOptions struct {
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

type ApplyOptions struct {
	ExtraArgs   []string `json:"extraArgs,omitempty"`
}

