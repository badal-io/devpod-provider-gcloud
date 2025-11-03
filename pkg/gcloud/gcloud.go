package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"github.com/googleapis/gax-go/v2/apierror"
	"github.com/loft-sh/devpod/pkg/client"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func NewClient(ctx context.Context, project, zone string, opts ...option.ClientOption) (*Client, error) {
	err := SetupEnvJson(ctx)
	if err != nil {
		return nil, err
	}

	instanceClient, err := compute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	routersClient, err := compute.NewRoutersRESTClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{
		InstanceClient: instanceClient,
		RoutersClient:  routersClient,
		Project:        project,
		Zone:           zone,
	}, nil
}

type Client struct {
	InstanceClient *compute.InstancesClient
	RoutersClient  *compute.RoutersClient

	Project string
	Zone    string
}

func SetupEnvJson(ctx context.Context) error {
	if os.Getenv("GCLOUD_JSON_AUTH") != "" {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		destination := filepath.Join(path.Dir(exePath), "gcloud_auth.json")

		f, err := os.OpenFile(destination, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = f.WriteString(os.Getenv("GCLOUD_JSON_AUTH"))
		if err != nil {
			return err
		}

		return os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", destination)
	}

	return nil
}

func DefaultTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
	}

	return google.DefaultTokenSource(ctx, scopes...)
}

func ParseToken(tok string) (*oauth2.Token, error) {
	oauthToken := &oauth2.Token{}
	err := json.Unmarshal([]byte(tok), oauthToken)
	if err != nil {
		return nil, err
	}

	return oauthToken, nil
}

func GetToken(ctx context.Context) ([]byte, error) {
	err := SetupEnvJson(ctx)
	if err != nil {
		return nil, err
	}

	tokSource, err := DefaultTokenSource(ctx)
	if err != nil {
		return nil, err
	}

	t, err := tokSource.Token()
	if err != nil {
		return nil, err
	}

	t.RefreshToken = ""
	t.TokenType = ""
	return json.Marshal(t)
}

func (c *Client) Init(ctx context.Context) error {
	_, err := c.InstanceClient.List(ctx, &computepb.ListInstancesRequest{
		Project: c.Project,
		Zone:    c.Zone,
	}).Next()
	if err != nil && err != iterator.Done {
		return fmt.Errorf("cannot list instances: %v", err)
	}

	return nil
}

func (c *Client) Create(ctx context.Context, instance *computepb.Instance) error {
	operation, err := c.InstanceClient.Insert(ctx, &computepb.InsertInstanceRequest{
		InstanceResource: instance,
		Project:          c.Project,
		Zone:             c.Zone,
	})
	if err != nil {
		return err
	}

	return operation.Wait(ctx)
}

func (c *Client) Start(ctx context.Context, name string) error {
	operation, err := c.InstanceClient.Start(ctx, &computepb.StartInstanceRequest{
		Instance: name,
		Project:  c.Project,
		Zone:     c.Zone,
	})
	if err != nil {
		return err
	}

	return operation.Wait(ctx)
}

func (c *Client) Stop(ctx context.Context, name string, async bool) error {
	operation, err := c.InstanceClient.Stop(ctx, &computepb.StopInstanceRequest{
		Instance: name,
		Project:  c.Project,
		Zone:     c.Zone,
	})
	if err != nil {
		return err
	} else if async {
		return nil
	}

	return operation.Wait(ctx)
}

func (c *Client) Delete(ctx context.Context, name string) error {
	operation, err := c.InstanceClient.Delete(ctx, &computepb.DeleteInstanceRequest{
		Instance: name,
		Project:  c.Project,
		Zone:     c.Zone,
	})
	if err != nil {
		return err
	}

	return operation.Wait(ctx)
}

func (c *Client) Get(ctx context.Context, name string) (*computepb.Instance, error) {
	instance, err := c.InstanceClient.Get(ctx, &computepb.GetInstanceRequest{
		Instance: name,
		Project:  c.Project,
		Zone:     c.Zone,
	})
	if err != nil {
		// check if api error
		apiError, ok := err.(*apierror.APIError)
		if ok {
			googleAPIError, ok := apiError.Unwrap().(*googleapi.Error)
			if ok && googleAPIError.Code == 404 {
				return nil, nil
			}
		}

		return nil, err
	}

	return instance, nil
}

func (c *Client) Status(ctx context.Context, name string) (client.Status, error) {
	instance, err := c.Get(ctx, name)
	if err != nil {
		return client.StatusNotFound, err
	} else if instance == nil {
		return client.StatusNotFound, nil
	}

	status := strings.TrimSpace(strings.ToUpper(*instance.Status))
	if status == "RUNNING" {
		return client.StatusRunning, nil
	} else if status == "STOPPING" || status == "SUSPENDING" || status == "REPAIRING" || status == "PROVISIONING" || status == "STAGING" {
		return client.StatusBusy, nil
	} else if status == "TERMINATED" {
		return client.StatusStopped, nil
	}

	return client.StatusNotFound, fmt.Errorf("unexpected status: %v", status)
}

func (c *Client) Close() error {
	err := c.InstanceClient.Close()
	if err != nil {
		return err
	}

	err = c.RoutersClient.Close()
	if err != nil {
		return err
	}

	return nil
}

// CheckCloudNAT checks if Cloud NAT is configured for the given subnet in the region
func (c *Client) CheckCloudNAT(ctx context.Context, region, subnetName string) (bool, error) {
	// List all routers in the region
	routersIterator := c.RoutersClient.List(ctx, &computepb.ListRoutersRequest{
		Project: c.Project,
		Region:  region,
	})

	// Check each router for NAT configuration on the specified subnet
	for {
		router, err := routersIterator.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return false, fmt.Errorf("error listing routers: %w", err)
		}

		// Check if this router has NAT configured
		if router.Nats != nil && len(router.Nats) > 0 {
			for _, nat := range router.Nats {
				// Check if this NAT applies to all subnets or specifically to our subnet
				if nat.SourceSubnetworkIpRangesToNat != nil {
					sourceType := *nat.SourceSubnetworkIpRangesToNat

					// ALL_SUBNETWORKS_ALL_IP_RANGES means Cloud NAT is enabled for all subnets
					if sourceType == "ALL_SUBNETWORKS_ALL_IP_RANGES" {
						return true, nil
					}

					// LIST_OF_SUBNETWORKS means we need to check if our subnet is in the list
					if sourceType == "LIST_OF_SUBNETWORKS" && nat.Subnetworks != nil {
						for _, subnet := range nat.Subnetworks {
							if subnet.Name != nil && strings.Contains(*subnet.Name, subnetName) {
								return true, nil
							}
						}
					}
				}
			}
		}
	}

	return false, nil
}
