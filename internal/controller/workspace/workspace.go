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

package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

	apisv1alpha1 "github.com/crossplane/provider-template/apis/v1alpha1"
	"github.com/crossplane/provider-template/internal/features"
)

const (
	errNotWorkspace = "managed resource is not a Workspace custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"
	errNewClient    = "cannot create new Service"
)

// ScalrService provides API operations for Scalr workspaces
type ScalrService struct {
	// These would be actual client fields in a real implementation
	apiToken   string
	apiBaseURL string
	httpClient *http.Client
}

var (
	newScalrService = func(creds []byte) (interface{}, error) {
		// Use the credentials passed from the provider config
		// The creds should contain the API token
		apiToken := string(creds)
		if len(apiToken) == 0 {
			return nil, errors.New(errGetCreds)
		}
		apiToken = strings.TrimSpace(apiToken)
		if strings.ContainsAny(apiToken, "\n\r\t") {
			return nil, fmt.Errorf("API token contains invalid characters")
		}

		return &ScalrService{
			apiToken:   apiToken,
			apiBaseURL: "https://essity.scalr.io",
			httpClient: &http.Client{},
		}, nil
	}
)

// Setup adds a controller that reconciles Workspace managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(apisv1alpha1.WorkspaceKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: newScalrService}),
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
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &apisv1alpha1.WorkspaceList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind apisv1alpha1.WorkspaceList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(apisv1alpha1.WorkspaceGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).WithEventFilter(resource.DesiredStateChanged()).
		For(&apisv1alpha1.Workspace{}).
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
	cr, ok := mg.(*apisv1alpha1.Workspace)
	if !ok {
		return nil, errors.New(errNotWorkspace)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	// Get the Provider credentials
	cd := pc.Spec.Credentials
	// Extract the credentials using the CommonCredentialExtractor
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}
	svc, err := c.newServiceFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{service: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	service interface{}
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*apisv1alpha1.Workspace)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotWorkspace)
	}

	fmt.Printf("Observe method called - checking workspace existence\n")
	fmt.Printf("Workspace Name: %s\n", *cr.Spec.ForProvider.Name)

	// If the object is being deleted, report that the external resource does not exist.
	if cr.GetDeletionTimestamp() != nil {
		fmt.Printf(">>> Resource is being deleted, reporting as non-existent\n")
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	// Get the Scalr client
	scalrClient := c.service.(*ScalrService)

	// Initialize variables
	workspaceExists := false
	var workspaceID string

	// fetch the workspace by name from the API
	externalName := *cr.Spec.ForProvider.Name
	if externalName != "" {
		fmt.Printf(">>> Checking workspace existence using external name: %s\n", externalName)
		// Query by name and environment
		url := fmt.Sprintf("%s/api/iacp/v3/workspaces?filter[name]=%s",
			scalrClient.apiBaseURL, externalName)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}

		// Add headers
		req.Header.Add("accept", "application/vnd.api+json")
		req.Header.Add("Authorization", "Bearer "+scalrClient.apiToken)

		// Send the request
		res, err := scalrClient.httpClient.Do(req)
		if err != nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		defer res.Body.Close()

		// Read response
		body, err := io.ReadAll(res.Body)
		if err != nil {
			fmt.Printf(">>> Error reading response body: %v\n", err)
			return managed.ExternalObservation{ResourceExists: false}, nil
		}

		// Log the API response
		// fmt.Printf(">>> API Response: %s\n", string(body))

		// Parse the response to check if workspace exists
		var response struct {
			Data []struct {
				ID         string `json:"id"`
				Attributes struct {
					Name string `json:"name"`
				} `json:"attributes"`
				Relationships struct {
					Environment struct {
						Data struct {
							ID string `json:"id"`
						} `json:"data"`
					} `json:"environment"`
				} `json:"relationships"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &response); err != nil {
			fmt.Printf(">>> Error unmarshalling response: %v\n", err)
			return managed.ExternalObservation{ResourceExists: false}, nil
		}

		// If we found matching workspaces
		if len(response.Data) > 0 {
			for _, workspace := range response.Data {
				// Check if environment matches if specified
				if cr.Spec.ForProvider.EnvironmentName != nil {
					envMatches := workspace.Relationships.Environment.Data.ID == *cr.Spec.ForProvider.EnvironmentName
					if !envMatches {
						continue
					}
				}

				// We found a matching workspace
				workspaceExists = true
				workspaceID = workspace.ID

				// Update the workspace ID in status
				cr.Status.AtProvider.WorkspaceId = &workspaceID
				fmt.Printf(">>> Found existing workspace by name: %s with ID: %s\n",
					workspace.Attributes.Name, workspaceID)
				break
			}
		}
	}

	// If we didn't find the workspace by external name, check if we have a workspace ID in the status
	if !workspaceExists && cr.Status.AtProvider.WorkspaceId != nil && *cr.Status.AtProvider.WorkspaceId != "" {
		fmt.Printf(">>> Checking workspace existence using ID from status: %s\n", *cr.Status.AtProvider.WorkspaceId)

		// Query by ID
		url := fmt.Sprintf("%s/api/iacp/v3/workspaces/%s",
			scalrClient.apiBaseURL, *cr.Status.AtProvider.WorkspaceId)

		// Create the HTTP request
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			fmt.Printf(">>> Error creating HTTP request: %v\n", err)
			return managed.ExternalObservation{ResourceExists: false}, nil
		}

		// Add headers
		req.Header.Add("accept", "application/vnd.api+json")
		req.Header.Add("Authorization", "Bearer "+scalrClient.apiToken)

		// Send the request
		res, err := scalrClient.httpClient.Do(req)
		if err != nil {
			fmt.Printf(">>> Error making HTTP request: %v\n", err)
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		defer res.Body.Close()

		// Check if the workspace exists based on response status
		if res.StatusCode == 200 {
			workspaceExists = true
			workspaceID = *cr.Status.AtProvider.WorkspaceId
			fmt.Printf(">>> Confirmed workspace exists by ID: %s\n", workspaceID)
		} else {
			fmt.Printf(">>> Workspace with ID %s does not exist (status: %d)\n",
				*cr.Status.AtProvider.WorkspaceId, res.StatusCode)
		}
	}

	fmt.Printf(">>> DEBUG: Workspace existence check result: %v\n", workspaceExists)

	if !workspaceExists {
		fmt.Printf(">>> Workspace does not exist, reporting ResourceExists: false\n")
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	// Since the workspace exists, we'll assume it's up-to-date
	// In a production implementation, you would compare the current state with desired state
	workspaceUpToDate := true
	fmt.Printf(">>> Workspace exists, assuming it's up-to-date\n")

	// Prepare connection details to return
	connectionDetails := managed.ConnectionDetails{}

	// Add workspace ID to connection details
	connectionDetails["workspace_id"] = []byte(workspaceID)

	// Add workspace name to connection details if available
	if cr.Spec.ForProvider.Name != nil {
		connectionDetails["workspace_name"] = []byte(*cr.Spec.ForProvider.Name)
	}

	// Add environment ID to connection details if available
	if cr.Spec.ForProvider.EnvironmentName != nil {
		connectionDetails["environment_name"] = []byte(*cr.Spec.ForProvider.EnvironmentName)
	}

	// Add a creation timestamp to connection details
	connectionDetails["observed_at"] = []byte(fmt.Sprintf("%d", time.Now().Unix()))

	fmt.Printf(">>> Observe result: Resource EXISTS, up-to-date=true, explicitly marking READY\n")

	// Set the resource as Available immediately
	cr.Status.SetConditions(xpv1.Available())

	// When a resource exists and is up-to-date, Crossplane sets the Ready condition to true
	// We need to make sure both ResourceExists and ResourceUpToDate are true
	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  workspaceUpToDate,
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*apisv1alpha1.Workspace)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotWorkspace)
	}

	scalrClient := c.service.(*ScalrService)

	// Build the API URL - use the client's configured base URL
	url := scalrClient.apiBaseURL + "/api/iacp/v3/workspaces"

	// Create the payload JSON
	payloadStr := fmt.Sprintf(`{
		"data": {
			"type": "workspaces",
			"attributes": {
				"name": "%s",
				"auto-apply": true
			},
			"relationships": {
				"environment": {
					"data": {
						"type": "environments",
						"id": "%s"
					}
				}
			}
		}
	}`, *cr.Spec.ForProvider.Name, *cr.Spec.ForProvider.EnvironmentName)

	// Create a reader for the payload
	payload := strings.NewReader(payloadStr)

	// Create the HTTP request
	req, err := http.NewRequest("POST", url, payload)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create HTTP request")
	}

	// Add the headers
	req.Header.Add("accept", "application/vnd.api+json")
	req.Header.Add("content-type", "application/vnd.api+json")

	// Set authorization if needed
	req.Header.Add("Authorization", "Bearer "+scalrClient.apiToken)

	// Log the request details before sending
	fmt.Printf("Sending API request to: %s\n", url)
	fmt.Printf("Request headers: %v\n", req.Header)

	res, err := scalrClient.httpClient.Do(req)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to create workspace")
	}
	defer res.Body.Close()

	// Read and process the response
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to read response body")
	}

	// Check for error responses
	if res.StatusCode >= 400 {
		// Special handling for 422 errors - check if it's because the workspace already exists
		if res.StatusCode == 422 && strings.Contains(string(body), "already in use") {
			fmt.Printf(">>> Workspace already exists, trying to find it by name\n")

			// Build the API URL to list workspaces by name and environment
			lookupURL := fmt.Sprintf("%s/api/iacp/v3/workspaces?filter[workspace][name]=%s",
				scalrClient.apiBaseURL, *cr.Spec.ForProvider.Name)

			if cr.Spec.ForProvider.EnvironmentName != nil {
				lookupURL = fmt.Sprintf("%s&filter[workspace][environment][id]=%s",
					lookupURL, *cr.Spec.ForProvider.EnvironmentName)
			}

			// Create the HTTP request
			lookupReq, err := http.NewRequest("GET", lookupURL, nil)
			if err != nil {
				return managed.ExternalCreation{}, errors.Wrap(err, "failed to create HTTP request for lookup")
			}

			// Add headers
			lookupReq.Header.Add("accept", "application/vnd.api+json")
			lookupReq.Header.Add("Authorization", "Bearer "+scalrClient.apiToken)

			// Send the request
			lookupRes, err := scalrClient.httpClient.Do(lookupReq)
			if err != nil {
				return managed.ExternalCreation{}, errors.Wrap(err, "failed to lookup existing workspace")
			}
			defer lookupRes.Body.Close()

			// Read response
			lookupBody, err := io.ReadAll(lookupRes.Body)
			if err != nil {
				return managed.ExternalCreation{}, errors.Wrap(err, "failed to read lookup response body")
			}

			// Parse the response to find the existing workspace
			var lookupResponse struct {
				Data []struct {
					ID         string `json:"id"`
					Attributes struct {
						Name string `json:"name"`
					} `json:"attributes"`
					Relationships struct {
						Environment struct {
							Data struct {
								ID string `json:"id"`
							} `json:"data"`
						} `json:"environment"`
					} `json:"relationships"`
				} `json:"data"`
			}

			if err := json.Unmarshal(lookupBody, &lookupResponse); err != nil {
				return managed.ExternalCreation{}, errors.Wrap(err, "failed to unmarshal lookup response")
			}

			// Find the matching workspace
			for _, workspace := range lookupResponse.Data {
				// Check if environment matches if specified
				if cr.Spec.ForProvider.EnvironmentName != nil {
					envMatches := workspace.Relationships.Environment.Data.ID == *cr.Spec.ForProvider.EnvironmentName
					if !envMatches {
						continue
					}
				}

				// We found a matching workspace
				workspaceID := workspace.ID
				fmt.Printf(">>> Found existing workspace '%s' with ID: %s\n",
					workspace.Attributes.Name, workspaceID)

				// Update the workspace ID in status
				cr.Status.AtProvider.WorkspaceId = &workspaceID

				// Return connection details for the existing workspace
				return managed.ExternalCreation{
					ConnectionDetails: managed.ConnectionDetails{
						"workspace_id": []byte(workspaceID),
					},
				}, nil
			}
		}

		// For other error cases or if workspace not found, return the error
		return managed.ExternalCreation{}, errors.Errorf("failed to create workspace: Status code: %d, Body: %s",
			res.StatusCode, string(body))
	}

	// Parse the response to get the workspace ID
	var response struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "failed to unmarshal response")
	}

	workspaceID := response.Data.ID
	fmt.Printf("Workspace Name: %s\n", *cr.Spec.ForProvider.Name)
	fmt.Printf("Workspace ID: %s\n", workspaceID)

	// Log response parameters
	fmt.Printf("Response status code: %d\n", res.StatusCode)
	fmt.Printf("Response body: %s\n", string(body))

	// Log workspace parameters from CR
	fmt.Printf("Parameters from CR:\n")
	if cr.Spec.ForProvider.Description != nil {
		fmt.Printf("  Description: %s\n", *cr.Spec.ForProvider.Description)
	} else {
		fmt.Printf("  Description: <nil>\n")
	}
	fmt.Printf("  Environment: %s\n", *cr.Spec.ForProvider.EnvironmentName)

	// Log the tags if present
	if len(cr.Spec.ForProvider.Tags) > 0 {
		fmt.Printf("  Tags:\n")
		for k, v := range cr.Spec.ForProvider.Tags {
			fmt.Printf("    %s: %s\n", k, v)
		}
	} else {
		fmt.Printf("  Tags: none\n")
	}

	// Update the status with the workspace ID
	cr.Status.AtProvider.WorkspaceId = &workspaceID

	return managed.ExternalCreation{
		// Return connection details for the new workspace
		ConnectionDetails: managed.ConnectionDetails{
			"workspace_id": []byte(workspaceID),
		},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*apisv1alpha1.Workspace)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotWorkspace)
	}

	fmt.Printf("Updating Scalr workspace: %s\n", *cr.Spec.ForProvider.Name)

	// Get service but don't use it directly in this example
	_ = c.service.(*ScalrService)
	workspaceID := ""

	// Get the workspace ID from status if available
	if cr.Status.AtProvider.WorkspaceId != nil {
		workspaceID = *cr.Status.AtProvider.WorkspaceId
	} else {
		// This shouldn't happen in practice as Update is called after Observe,
		// which should set the ID, but we handle it for safety
		workspaceID = "ws-" + *cr.Spec.ForProvider.Name + "-12345"
	}

	// Update workspace in Scalr using the IACP v3 API
	//
	// The API endpoint is: PATCH https://example.scalr.io/api/iacp/v3/workspaces/{workspaceID}
	//
	// Example request body:
	// {
	//   "data": {
	//     "attributes": {
	//       "auto-queue-runs": "skip_first",
	//       "execution-mode": "remote",
	//       "iac-platform": "terraform",
	//       "terragrunt": {
	//         "include-external-dependencies": false,
	//         "use-run-all": false
	//       },
	//       "vcs-repo": {
	//         "dry-runs-enabled": true,
	//         "ingress-submodules": false
	//       }
	//     },
	//     "relationships": {
	//       "agent-pool": {"data": {"type": "agent-pools"}},
	//       "environment": {"data": {"type": "environments"}},
	//       "module-version": {"data": {"type": "module-versions"}},
	//       "vcs-provider": {"data": {"type": "vcs-providers"}}
	//     },
	//     "type": "workspaces"
	//   }
	// }

	// In a production implementation, this would use the API directly:
	//
	// url := fmt.Sprintf("%s/api/iacp/v3/workspaces/%s", scalrClient.apiBaseURL, workspaceID)
	// payload := buildUpdateWorkspacePayload(cr)
	// req, _ := http.NewRequest("PATCH", url, strings.NewReader(payload))
	// req.Header.Add("accept", "application/vnd.api+json")
	// req.Header.Add("content-type", "application/vnd.api+json")
	// res, err := httpClient.Do(req)
	// if err != nil {
	//     return managed.ExternalUpdate{}, errors.Wrap(err, "failed to update workspace")
	// }

	fmt.Printf("Would make API call to https://example.scalr.io/api/iacp/v3/workspaces/%s to update workspace\n",
		workspaceID)

	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{
			"workspace_id": []byte(workspaceID),
		},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*apisv1alpha1.Workspace)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotWorkspace)
	}

	fmt.Printf("###########################################################\n")
	fmt.Printf("DELETE METHOD CALLED - BEGINNING WORKSPACE DELETION\n")
	fmt.Printf("###########################################################\n")
	fmt.Printf("Deleting Scalr workspace: %s\n", *cr.Spec.ForProvider.Name)

	// Get service but don't use it directly in this example
	_ = c.service.(*ScalrService)
	workspaceID := ""

	// Get the workspace ID from status if available
	if cr.Status.AtProvider.WorkspaceId != nil {
		workspaceID = *cr.Status.AtProvider.WorkspaceId
	} else {
		// If we don't have an ID, this might be a cleanup of a failed creation
		fmt.Println("No workspace ID found in status, assuming resource doesn't exist")
		return managed.ExternalDelete{}, nil
	}

	// Delete workspace in Scalr using the IACP v3 API
	//
	// The API endpoint is: DELETE https://example.scalr.io/api/iacp/v3/workspaces/{workspaceID}
	//
	// In a production implementation, this would use the API directly:
	//
	// url := fmt.Sprintf("https://example.scalr.io/api/iacp/v3/workspaces/%s", workspaceID)
	// req, _ := http.NewRequest("DELETE", url, nil)
	// req.Header.Add("accept", "application/vnd.api+json")
	// res, err := httpClient.Do(req)
	// if err != nil {
	//     return managed.ExternalDelete{}, errors.Wrap(err, "failed to delete workspace")
	// }

	fmt.Printf("Would make API call to https://example.scalr.io/api/iacp/v3/workspaces/%s to delete workspace\n",
		workspaceID)

	// No connection details needed for deletion
	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	// Clean up any API client resources
	// For a REST API client, this might be a no-op
	fmt.Println("Disconnecting from Scalr API")
	// In a real implementation with persistent connections,
	// you might clean up resources here
	return nil
}
