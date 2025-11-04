package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/badal-io/devpod-provider-gcloud/pkg/gcloud"
	"github.com/badal-io/devpod-provider-gcloud/pkg/options"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/ssh"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// CommandCmd holds the cmd flags
type CommandCmd struct{}

// NewCommandCmd defines a command
func NewCommandCmd() *cobra.Command {
	cmd := &CommandCmd{}
	commandCmd := &cobra.Command{
		Use:   "command",
		Short: "Run a command on the instance",
		RunE: func(_ *cobra.Command, args []string) error {
			options, err := options.FromEnv(true, true)
			if err != nil {
				return err
			}

			return cmd.Run(context.Background(), options, log.Default)
		},
	}

	return commandCmd
}

// Run runs the command logic
func (cmd *CommandCmd) Run(ctx context.Context, options *options.Options, log log.Logger) error {
	command := os.Getenv("COMMAND")
	if command == "" {
		return fmt.Errorf("command environment variable is missing")
	}

	// get private key
	privateKey, err := ssh.GetPrivateKeyRawBase(options.MachineFolder)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	// create gcloud client
	client, err := gcloud.NewClient(ctx, options.Project, options.Zone)
	if err != nil {
		return err
	}
	defer client.Close()

	// get instance
	instance, err := client.Get(ctx, options.MachineID)
	if err != nil {
		return err
	} else if instance == nil {
		return fmt.Errorf("instance %s doesn't exist", options.MachineID)
	}

	// get external ip
	if options.PublicIP && (len(instance.NetworkInterfaces) == 0 || len(instance.NetworkInterfaces[0].AccessConfigs) == 0 || instance.NetworkInterfaces[0].AccessConfigs[0].NatIP == nil) {
		return fmt.Errorf("instance %s doesn't have an external nat ip", options.MachineID)
	}

	// Use SSH with ProxyCommand for IAP when no public IP
	if !options.PublicIP {
		// Path to SSH config file created during machine setup
		sshConfigPath := filepath.Join(options.MachineFolder, "ssh_config")

		// Use system ssh command with our config file
		// This leverages the ProxyCommand configured during create
		sshArgs := []string{
			"-F", sshConfigPath,  // Use our SSH config with ProxyCommand
			options.MachineID,    // Host (configured in ssh_config)
			command,              // Command to execute
		}

		sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
		sshCmd.Stdin = os.Stdin
		sshCmd.Stdout = os.Stdout
		sshCmd.Stderr = os.Stderr

		if err := sshCmd.Run(); err != nil {
			return fmt.Errorf("ssh via IAP ProxyCommand: %w", err)
		}

		return nil
	}

	// For instances with public IP, use standard SSH
	target := *instance.NetworkInterfaces[0].AccessConfigs[0].NatIP
	port := "22"

	sshClient, err := ssh.NewSSHClient("devpod", target+":"+port, privateKey)
	if err != nil {
		return errors.Wrap(err, "create ssh client")
	}
	defer sshClient.Close()

	// run command
	return ssh.Run(ctx, sshClient, command, os.Stdin, os.Stdout, os.Stderr)
}

func findAvailablePort() (string, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	defer l.Close()

	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port), nil
}

func waitForPort(ctx context.Context, port string) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		default:
			l, err := net.Listen("tcp", "localhost:"+port)
			if err != nil {
				// port is taken (tunnel is ready)
				return true
			}
			_ = l.Close()
			time.Sleep(1 * time.Second)
		}
	}
}
