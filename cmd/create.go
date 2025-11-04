package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/badal-io/devpod-provider-gcloud/pkg/gcloud"
	"github.com/badal-io/devpod-provider-gcloud/pkg/options"
	"github.com/badal-io/devpod-provider-gcloud/pkg/ptr"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/ssh"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// CreateCmd holds the cmd flags
type CreateCmd struct{}

// NewCreateCmd defines a command
func NewCreateCmd() *cobra.Command {
	cmd := &CreateCmd{}
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		RunE: func(_ *cobra.Command, args []string) error {
			options, err := options.FromEnv(true, true)
			if err != nil {
				return err
			}

			return cmd.Run(context.Background(), options, log.Default)
		},
	}

	return createCmd
}

// Run runs the command logic
func (cmd *CreateCmd) Run(ctx context.Context, options *options.Options, log log.Logger) error {
	client, err := gcloud.NewClient(ctx, options.Project, options.Zone)
	if err != nil {
		return err
	}
	defer client.Close()

	// Check Cloud NAT and IAP configuration if using private IP (IAP)
	if !options.PublicIP {
		err = checkCloudNATConfiguration(ctx, client, options)
		if err != nil {
			return err
		}

		err = checkIAPFirewallRules(ctx, options, log)
		if err != nil {
			log.Warnf("IAP firewall check: %v", err)
			log.Info("Continuing anyway - you may need to configure IAP firewall rules manually if connection fails")
		}
	}

	instance, err := buildInstance(options)
	if err != nil {
		return err
	}

	err = client.Create(ctx, instance)
	if err != nil {
		return err
	}

	// Configure SSH with ProxyCommand for IAP if not using public IP
	if !options.PublicIP {
		// Wait for instance to be fully ready and startup script to complete
		log.Info("Waiting for instance to be fully ready...")
		if err := waitForInstanceReady(ctx, client, options, log); err != nil {
			return fmt.Errorf("waiting for instance ready: %w", err)
		}

		return configureSSHForIAP(options)
	}

	return nil
}

func buildInstance(options *options.Options) (*computepb.Instance, error) {
	diskSize, err := strconv.Atoi(options.DiskSize)
	if err != nil {
		return nil, errors.Wrap(err, "parse disk size")
	}

	// generate ssh keys
	publicKeyBase, err := ssh.GetPublicKeyBase(options.MachineFolder)
	if err != nil {
		return nil, errors.Wrap(err, "generate public key")
	}

	publicKey, err := base64.StdEncoding.DecodeString(publicKeyBase)
	if err != nil {
		return nil, err
	}
	serviceAccounts := []*computepb.ServiceAccount{}
	if options.ServiceAccount != "" {
		serviceAccounts = []*computepb.ServiceAccount{
			{
				Email: &options.ServiceAccount,
				Scopes: []string{
					"https://www.googleapis.com/auth/cloud-platform",
				},
			},
		}
	}

	// prepare metadata items
	metadataItems := []*computepb.Items{
		{
			Key:   ptr.Ptr("ssh-keys"),
			Value: ptr.Ptr("devpod:" + string(publicKey)),
		},
	}

	// Add startup script for IAP (no public IP) to create devpod user
	// Google's guest-agent doesn't auto-create users from metadata when connecting via IAP
	if !options.PublicIP {
		startupScript := `#!/bin/bash
# Create devpod user if it doesn't exist (required for IAP SSH)
if ! id -u devpod > /dev/null 2>&1; then
  useradd -m -s /bin/bash devpod
  usermod -aG sudo devpod
  # Allow sudo without password for DevPod operations
  echo "devpod ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/devpod
  chmod 0440 /etc/sudoers.d/devpod

  # Setup SSH authorized_keys from metadata
  # Google's guest-agent doesn't populate this for IAP connections
  mkdir -p /home/devpod/.ssh
  chmod 700 /home/devpod/.ssh

  # Extract devpod's public key from instance metadata
  curl -s "http://metadata.google.internal/computeMetadata/v1/instance/attributes/ssh-keys" \
    -H "Metadata-Flavor: Google" | \
    grep "^devpod:" | \
    sed 's/^devpod://' > /home/devpod/.ssh/authorized_keys

  chmod 600 /home/devpod/.ssh/authorized_keys
  chown -R devpod:devpod /home/devpod/.ssh
fi
`
		metadataItems = append(metadataItems, &computepb.Items{
			Key:   ptr.Ptr("startup-script"),
			Value: ptr.Ptr(startupScript),
		})
	}

	// generate instance object
	instance := &computepb.Instance{
		Scheduling: &computepb.Scheduling{
			AutomaticRestart:  ptr.Ptr(true),
			OnHostMaintenance: ptr.Ptr(getMaintenancePolicy(options.MachineType)),
		},
		Metadata: &computepb.Metadata{
			Items: metadataItems,
		},
		MachineType: ptr.Ptr(fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", options.Project, options.Zone, options.MachineType)),
		Disks: []*computepb.AttachedDisk{
			{
				AutoDelete: ptr.Ptr(true),
				Boot:       ptr.Ptr(true),
				DeviceName: ptr.Ptr(options.MachineID),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					DiskSizeGb:  ptr.Ptr(int64(diskSize)),
					DiskType:    ptr.Ptr(fmt.Sprintf("projects/%s/zones/%s/diskTypes/pd-balanced", options.Project, options.Zone)),
					SourceImage: ptr.Ptr(options.DiskImage),
				},
			},
		},
		Tags: buildInstanceTags(options),
		NetworkInterfaces: []*computepb.NetworkInterface{
			{
				Network:       normalizeNetworkID(options),
				Subnetwork:    normalizeSubnetworkID(options),
				AccessConfigs: getAccessConfig(options),
			},
		},
		Zone:            ptr.Ptr(fmt.Sprintf("projects/%s/zones/%s", options.Project, options.Zone)),
		Name:            ptr.Ptr(options.MachineID),
		ServiceAccounts: serviceAccounts,
	}

	return instance, nil
}

func getAccessConfig(options *options.Options) []*computepb.AccessConfig {
	if options.PublicIP {
		return []*computepb.AccessConfig{
			{
				Name:        ptr.Ptr("External NAT"),
				NetworkTier: ptr.Ptr("STANDARD"),
			},
		}
	}

	return nil
}

func buildInstanceTags(options *options.Options) *computepb.Tags {
	if len(options.Tag) == 0 {
		return nil
	}

	return &computepb.Tags{Items: []string{options.Tag}}
}

func normalizeNetworkID(options *options.Options) *string {
	network := options.Network
	project := options.Project

	if len(network) == 0 {
		return nil
	}

	// projects/{{project}}/regions/{{region}}/subnetworks/{{name}}
	if regexp.MustCompile("projects/([^/]+)/global/networks/([^/]+)").MatchString(network) {
		return ptr.Ptr(network)
	}

	// {{project}}/{{name}}
	if regexp.MustCompile("([^/]+)/([^/]+)").MatchString(network) {
		s := strings.Split(network, "/")
		return ptr.Ptr(fmt.Sprintf("projects/%s/global/networks/%s", s[0], s[1]))
	}

	// {{name}}
	return ptr.Ptr(fmt.Sprintf("projects/%s/global/networks/%s", project, network))
}

func normalizeSubnetworkID(options *options.Options) *string {
	sn := strings.TrimSpace(options.Subnetwork)

	if len(sn) == 0 {
		return nil
	}

	project := options.Project
	zone := options.Zone
	region := zone[:strings.LastIndex(zone, "-")]

	// projects/{{project}}/regions/{{region}}/subnetworks/{{name}}
	if regexp.MustCompile("projects/([^/]+)/regions/([^/]+)/subnetworks/([^/]+)").MatchString(sn) {
		return ptr.Ptr(sn)
	}

	// {{project}}/{{region}}/{{name}}
	if regexp.MustCompile("([^/]+)/([^/]+)/([^/]+)").MatchString(sn) {
		s := strings.Split(sn, "/")
		return ptr.Ptr(fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", s[0], s[1], s[2]))
	}

	// {{region}}/{{name}}
	if regexp.MustCompile("([^/]+)/([^/]+)").MatchString(sn) {
		s := strings.Split(sn, "/")
		return ptr.Ptr(fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", project, s[0], s[1]))
	}

	// {{name}}
	return ptr.Ptr(fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", project, region, sn))
}

var gpuInstancePattern *regexp.Regexp = regexp.MustCompile(`^[agn][0-9]`)

func getMaintenancePolicy(machineType string) string {
	if gpuInstancePattern.MatchString(machineType) {
		return "TERMINATE"
	}

	return "MIGRATE"
}

// checkCloudNATConfiguration verifies that Cloud NAT is configured for the subnet when using private IPs
func checkCloudNATConfiguration(ctx context.Context, client *gcloud.Client, options *options.Options) error {
	// Extract region from zone (zone format: us-central1-a -> region: us-central1)
	zone := options.Zone
	region := zone[:strings.LastIndex(zone, "-")]

	// Extract subnet name from the configured subnetwork
	// If no subnetwork is specified, we can't check Cloud NAT
	if options.Subnetwork == "" {
		return fmt.Errorf("subnetwork must be specified when using private IP (PUBLIC_IP=false)")
	}

	// Parse the subnet name from various possible formats
	subnetName := options.Subnetwork
	// Handle full resource path: projects/{project}/regions/{region}/subnetworks/{name}
	if strings.Contains(subnetName, "/subnetworks/") {
		parts := strings.Split(subnetName, "/")
		subnetName = parts[len(parts)-1]
	}
	// Handle {region}/{name} format
	if strings.Contains(subnetName, "/") && !strings.Contains(subnetName, "projects/") {
		parts := strings.Split(subnetName, "/")
		subnetName = parts[len(parts)-1]
	}

	// Check if Cloud NAT is configured for this subnet
	hasCloudNAT, err := client.CheckCloudNAT(ctx, region, subnetName)
	if err != nil {
		return fmt.Errorf("failed to check Cloud NAT configuration: %w", err)
	}

	if !hasCloudNAT {
		return fmt.Errorf(`Cloud NAT is not configured for subnet '%s' in region '%s'.

DevPod instances without public IPs require Cloud NAT for outbound internet access
to download the DevPod agent and dependencies.

To enable Cloud NAT, run the following commands:

  # Create a Cloud Router (if one doesn't exist)
  gcloud compute routers create devpod-nat-router \
    --project=%s \
    --region=%s \
    --network=%s

  # Create Cloud NAT configuration
  gcloud compute routers nats create devpod-nat-config \
    --router=devpod-nat-router \
    --region=%s \
    --nat-all-subnet-ip-ranges \
    --auto-allocate-nat-external-ips \
    --project=%s

Alternatively, to configure Cloud NAT for a specific subnet only:

  gcloud compute routers nats create devpod-nat-config \
    --router=devpod-nat-router \
    --region=%s \
    --nat-custom-subnet-ip-ranges=%s \
    --auto-allocate-nat-external-ips \
    --project=%s

For more information, see:
https://cloud.google.com/nat/docs/gke-example#step_1_create_a_nat_configuration_using`,
			subnetName,
			region,
			options.Project,
			region,
			options.Network,
			region,
			options.Project,
			region,
			subnetName,
			options.Project,
		)
	}

	return nil
}

// configureSSHForIAP creates an SSH config file with ProxyCommand for IAP tunneling
func configureSSHForIAP(options *options.Options) error {
	// SSH config will be in the machine folder
	sshConfigPath := filepath.Join(options.MachineFolder, "ssh_config")

	// Create SSH config content with ProxyCommand for IAP
	// Using ConnectTimeout and longer ServerAlive settings for IAP
	sshConfig := fmt.Sprintf(`# DevPod GCP Provider IAP SSH Configuration
Host %s
    HostName %s
    User devpod
    IdentityFile %s
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    ProxyCommand gcloud compute start-iap-tunnel %%h %%p --listen-on-stdin --project=%s --zone=%s --verbosity=warning
    ConnectTimeout 60
    ServerAliveInterval 30
    ServerAliveCountMax 10
    TCPKeepAlive yes
`,
		options.MachineID,            // Host
		options.MachineID,            // HostName (will be resolved via ProxyCommand)
		filepath.Join(options.MachineFolder, "id_devpod_rsa"), // IdentityFile - DevPod's key naming
		options.Project,              // GCP Project
		options.Zone,                 // GCP Zone
	)

	// Write SSH config file
	if err := os.WriteFile(sshConfigPath, []byte(sshConfig), 0600); err != nil {
		return fmt.Errorf("write ssh config: %w", err)
	}

	return nil
}

// checkIAPFirewallRules verifies or provides guidance on IAP firewall rules
func checkIAPFirewallRules(ctx context.Context, options *options.Options, log log.Logger) error {
	log.Info("Checking IAP firewall configuration...")

	// Check if IAP firewall rule exists using gcloud command
	checkCmd := exec.CommandContext(ctx, "gcloud", "compute", "firewall-rules", "list",
		"--project="+options.Project,
		"--filter=name~devpod-allow-iap OR (sourceRanges:35.235.240.0/20 AND allowed:tcp:22)",
		"--format=value(name)")

	output, err := checkCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check firewall rules - ensure gcloud CLI is installed and configured")
	}

	if len(strings.TrimSpace(string(output))) > 0 {
		log.Info("IAP firewall rules are configured")
		return nil
	}

	// Firewall rule doesn't exist - provide instructions
	return fmt.Errorf(`IAP firewall rule not found. DevPod needs a firewall rule to allow IAP SSH access.

To create the firewall rule, run:

  gcloud compute firewall-rules create devpod-allow-iap \\
    --project=%s \\
    --direction=INGRESS \\
    --priority=1000 \\
    --network=%s \\
    --action=ALLOW \\
    --rules=tcp:22 \\
    --source-ranges=35.235.240.0/20 \\
    --target-tags=%s

Or if not using tags:

  gcloud compute firewall-rules create devpod-allow-iap \\
    --project=%s \\
    --direction=INGRESS \\
    --priority=1000 \\
    --network=%s \\
    --action=ALLOW \\
    --rules=tcp:22 \\
    --source-ranges=35.235.240.0/20

The source range 35.235.240.0/20 is Google's IAP forwarding range.
For more info: https://cloud.google.com/iap/docs/using-tcp-forwarding#create-firewall-rule`,
		options.Project,
		options.Network,
		options.Tag,
		options.Project,
		options.Network,
	)
}

// waitForInstanceReady waits for the instance to be fully ready including startup script completion
func waitForInstanceReady(ctx context.Context, client *gcloud.Client, options *options.Options, log log.Logger) error {
	// First, wait for instance to be in RUNNING state
	maxAttempts := 60 // 5 minutes (60 * 5 seconds)
	for i := 0; i < maxAttempts; i++ {
		status, err := client.Status(ctx, options.MachineID)
		if err != nil {
			return fmt.Errorf("check instance status: %w", err)
		}

		if status == "Running" {
			break
		}

		if i == maxAttempts-1 {
			return fmt.Errorf("timeout waiting for instance to be running")
		}

		time.Sleep(5 * time.Second)
	}

	log.Info("Instance is running, waiting for startup script to complete...")

	// Wait additional time for startup script to create devpod user
	// The startup script typically takes 10-30 seconds
	time.Sleep(30 * time.Second)

	// Verify devpod user exists by attempting a quick SSH connection test
	// This will fail if the user doesn't exist yet
	sshConfigPath := filepath.Join(options.MachineFolder, "ssh_config")
	testCmd := exec.CommandContext(ctx, "ssh",
		"-F", sshConfigPath,
		"-o", "ConnectTimeout=10",
		options.MachineID,
		"echo 'ready'")

	// Try up to 6 times (1 minute total with 10s timeout each)
	for i := 0; i < 6; i++ {
		if err := testCmd.Run(); err == nil {
			log.Info("Instance is fully ready for SSH connections")
			return nil
		}

		if i < 5 {
			log.Infof("Waiting for SSH to be ready (attempt %d/6)...", i+1)
			time.Sleep(10 * time.Second)
		}
	}

	// Don't fail here - continue anyway as the instance might still work
	log.Warn("SSH readiness check timed out, but continuing anyway...")
	return nil
}
