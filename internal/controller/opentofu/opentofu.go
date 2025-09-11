package opentofu

import (
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var log = logging.NewLogrLogger(zap.New(zap.UseDevMode(true)))


func runCommand() {
	log.Info("Starting the OpenTofu controller")
}

func InitCommand(opts InitOptions) []string {
	cmd := []string{"init"}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}


func PlanCommand(opts PlanOptions) []string {
	cmd := []string{"plan", "-out=tfplan"}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}


func ApplyCommand(opts ApplyOptions) []string {
	cmd := []string{"apply", "-input=false", "tfplan"}
	cmd = append(cmd, opts.ExtraArgs...)
	return cmd
}

