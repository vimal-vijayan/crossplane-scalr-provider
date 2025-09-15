/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crossplane/crossplane-runtime/pkg/feature"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/statemetrics"

	runner "github.com/crossplane/provider-template/apis/runner/v1alpha1"
	apisv1alpha1 "github.com/crossplane/provider-template/apis/v1alpha1"

	opentofu "github.com/crossplane/provider-template/internal/controller/opentofu"
	"github.com/crossplane/provider-template/internal/features"
)

const (
	errNotMyType    = "managed resource is not a Runner custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
	
	// Runner finalizer
	runnerFinalizer = "runner.scalr.essity.com/finalizer"
)

// A TofuService manages OpenTofu operations.
type TofuService struct {
	runner opentofu.Runner
}

// NewTofuService creates a new TofuService
func NewTofuService() *TofuService {
	return &TofuService{
		runner: opentofu.NewDefaultRunner(),
	}
}

var (
	newTofuService = func(_ []byte) (interface{}, error) { return NewTofuService(), nil }
)

// Setup adds a controller that reconciles Runner managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(runner.RunnerGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newTofuService}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		managed.WithManagementPolicies(),
	}

	if o.Features.Enabled(feature.EnableAlphaChangeLogs) {
		opts = append(opts, managed.WithChangeLogger(o.ChangeLogOptions.ChangeLogger))
	}

	if o.MetricOptions != nil {
		opts = append(opts, managed.WithMetricRecorder(o.MetricOptions.MRMetrics))
	}

	if o.MetricOptions != nil && o.MetricOptions.MRStateMetrics != nil {
		stateMetricsRecorder := statemetrics.NewMRStateRecorder(
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &runner.RunnerList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.RunnerList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(runner.RunnerGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&runner.Runner{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (interface{}, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*runner.Runner)
	if !ok {
		return nil, errors.New(errNotMyType)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	tofuService, ok := svc.(*TofuService)
	if !ok {
		return nil, errors.New("service is not a TofuService")
	}

	return &external{service: tofuService}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	// TofuService for managing OpenTofu operations
	service *TofuService
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*runner.Runner)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotMyType)
	}

	// Validate required fields
	if cr.Spec.ForProvider.Name == nil {
		return managed.ExternalObservation{}, errors.New("runner name is required")
	}

	name := *cr.Spec.ForProvider.Name
	workingDir := c.getWorkingDir(name)

	// Check if deletion is in progress
	if cr.GetDeletionTimestamp() != nil {
		fmt.Printf("Resource %s is marked for deletion\n", name)
		
		// Check if working directory exists
		if _, err := os.Stat(workingDir); os.IsNotExist(err) {
			// Already cleaned up
			return managed.ExternalObservation{
				ResourceExists:   false,
				ResourceUpToDate: false,
			}, nil
		}
		
		// Run tofu plan -destroy -detailed-exitcode to check if there's anything to destroy
		driver := "opentofu" // default
		if cr.Spec.ForProvider.Driver != nil {
			driver = *cr.Spec.ForProvider.Driver
		}
		
		planResult, err := c.service.runner.Plan(ctx, opentofu.PlanOptions{
			WorkingDir:       workingDir,
			DetailedExitCode: true,
			Destroy:          true,
			Variables:        cr.Spec.ForProvider.Vars,
			Environment:      cr.Spec.ForProvider.Env,
			Driver:           driver,
		})
		
		if err != nil {
			return managed.ExternalObservation{}, fmt.Errorf("failed to run destroy plan: %w", err)
		}
		
		// Exit code 2 means changes are present (something to destroy)
		// Exit code 0 means no changes (nothing to destroy)
		if planResult.ExitCode == 0 {
			// Nothing to destroy, can complete deletion
			return managed.ExternalObservation{
				ResourceExists:   false,
				ResourceUpToDate: false,
			}, nil
		} else if planResult.ExitCode == 2 {
			// Something to destroy
			return managed.ExternalObservation{
				ResourceExists:   true,
				ResourceUpToDate: false,
			}, nil
		} else {
			return managed.ExternalObservation{}, fmt.Errorf("destroy plan failed with exit code %d: %s", planResult.ExitCode, planResult.Stderr)
		}
	}

	// Check if working directory exists (indicates resource was created)
	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		// Resource doesn't exist yet
		return managed.ExternalObservation{
			ResourceExists:   false,
			ResourceUpToDate: false,
		}, nil
	}

	// Run tofu plan -detailed-exitcode to check if changes are needed
	planResult, err := c.service.runner.Plan(ctx, opentofu.PlanOptions{
		WorkingDir:       workingDir,
		DetailedExitCode: true,
		Variables:        cr.Spec.ForProvider.Vars,
		Environment:      cr.Spec.ForProvider.Env,
	})
	
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("failed to run plan: %w", err)
	}

	// Exit code 0 means no changes needed
	// Exit code 2 means changes are present
	upToDate := planResult.ExitCode == 0
	
	if planResult.ExitCode != 0 && planResult.ExitCode != 2 {
		return managed.ExternalObservation{}, fmt.Errorf("plan failed with exit code %d: %s", planResult.ExitCode, planResult.Stderr)
	}

	fmt.Printf("Runner %s exists, up-to-date: %t\n", name, upToDate)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*runner.Runner)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotMyType)
	}

	name := *cr.Spec.ForProvider.Name
	workingDir := c.getWorkingDir(name)
	
	fmt.Printf("Creating runner: %s\n", name)

	// Step 1: Clone the repository
	cloneOpts := opentofu.CloneOptions{
		Repository: cr.Spec.ForProvider.Source,
		Directory:  workingDir,
	}
	
	if err := c.service.runner.CloneRepository(ctx, cloneOpts); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("failed to clone repository: %w", err)
	}

	// Step 2: Apply with auto-approve (init will be called automatically)
	applyOpts := opentofu.ApplyOptions{
		WorkingDir:  workingDir,
		AutoApprove: true,
		Environment: cr.Spec.ForProvider.Env,
	}
	
	applyResult, err := c.service.runner.Apply(ctx, applyOpts)
	if err != nil || applyResult.ExitCode != 0 {
		return managed.ExternalCreation{}, fmt.Errorf("failed to apply tofu configuration: %w, output: %s", err, applyResult.Stderr)
	}

	fmt.Printf("Successfully created runner: %s\n", name)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*runner.Runner)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotMyType)
	}

	name := *cr.Spec.ForProvider.Name
	workingDir := c.getWorkingDir(name)
	
	fmt.Printf("Updating runner: %s\n", name)

	// Apply changes (OpenTofu will automatically detect what needs to be updated)
	applyOpts := opentofu.ApplyOptions{
		WorkingDir:  workingDir,
		AutoApprove: true,
		Environment: cr.Spec.ForProvider.Env,
	}
	
	applyResult, err := c.service.runner.Apply(ctx, applyOpts)
	if err != nil || applyResult.ExitCode != 0 {
		return managed.ExternalUpdate{}, fmt.Errorf("failed to update tofu configuration: %w, output: %s", err, applyResult.Stderr)
	}

	fmt.Printf("Successfully updated runner: %s\n", name)
	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*runner.Runner)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotMyType)
	}

	// Validate required fields
	if cr.Spec.ForProvider.Name == nil {
		return managed.ExternalDelete{}, errors.New("runner name is required for deletion")
	}

	runnerName := *cr.Spec.ForProvider.Name
	workingDir := c.getWorkingDir(runnerName)
	
	fmt.Printf("Starting deletion process for runner: %s\n", runnerName)

	// Check if working directory exists
	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		fmt.Printf("Working directory does not exist, deletion complete: %s\n", runnerName)
		return managed.ExternalDelete{}, nil
	}

	// Step 1: Run tofu destroy with auto-approve
	destroyOpts := opentofu.DestroyOptions{
		WorkingDir:  workingDir,
		AutoApprove: true,
		Variables:   cr.Spec.ForProvider.Vars,
		Environment: cr.Spec.ForProvider.Env,
	}
	
	destroyResult, err := c.service.runner.Destroy(ctx, destroyOpts)
	if err != nil {
		return managed.ExternalDelete{}, fmt.Errorf("failed to destroy tofu resources: %w", err)
	}
	
	if destroyResult.ExitCode != 0 {
		return managed.ExternalDelete{}, fmt.Errorf("destroy failed with exit code %d: %s", destroyResult.ExitCode, destroyResult.Stderr)
	}

	// Step 2: Check if anything is left to destroy using plan -destroy -detailed-exitcode
	planResult, err := c.service.runner.Plan(ctx, opentofu.PlanOptions{
		WorkingDir:       workingDir,
		DetailedExitCode: true,
		Destroy:          true,
		Variables:        cr.Spec.ForProvider.Vars,
		Environment:      cr.Spec.ForProvider.Env,
	})
	
	if err != nil {
		return managed.ExternalDelete{}, fmt.Errorf("failed to run destroy plan check: %w", err)
	}

	// If exit code is 2, there are still resources to destroy
	if planResult.ExitCode == 2 {
		fmt.Printf("Still resources to destroy for runner: %s, running destroy again\n", runnerName)
		
		// Run destroy again
		destroyResult, err = c.service.runner.Destroy(ctx, destroyOpts)
		if err != nil {
			return managed.ExternalDelete{}, fmt.Errorf("failed to destroy remaining tofu resources: %w", err)
		}
		
		if destroyResult.ExitCode != 0 {
			return managed.ExternalDelete{}, fmt.Errorf("second destroy failed with exit code %d: %s", destroyResult.ExitCode, destroyResult.Stderr)
		}
	}

	// Step 3: Clean up the working directory
	if err := c.service.runner.Cleanup(ctx, workingDir); err != nil {
		fmt.Printf("Warning: failed to cleanup working directory %s: %v\n", workingDir, err)
		// Don't fail deletion due to cleanup failure
	}

	fmt.Printf("Successfully deleted runner: %s\n", runnerName)
	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// getWorkingDir returns the working directory for a runner
func (c *external) getWorkingDir(name string) string {
	return filepath.Join("/tmp", "tofu-runners", name)
}