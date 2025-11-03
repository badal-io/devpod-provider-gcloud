# GCLOUD Provider for DevPod
## Enhanced with IAP Support and Cloud NAT Detection

[![Join us on Slack!](docs/static/media/slack.svg)](https://slack.loft.sh/) [![Open in DevPod!](https://devpod.sh/assets/open-in-devpod.svg)](https://devpod.sh/open#https://github.com/badal-io/devpod-provider-gcloud)

This is an enhanced version of the DevPod GCloud provider with additional features:
- **Identity-Aware Proxy (IAP) SSH Support**: Connect to VMs without public IPs using Google Cloud IAP
- **Cloud NAT Detection**: Automatic validation of Cloud NAT configuration for private instances
- **ProxyCommand Integration**: Seamless SSH tunneling through IAP for persistent connections

## Getting started

The provider is available for installation from this repository using:

```sh
devpod provider add gcloud --url https://github.com/badal-io/devpod-provider-gcloud -o PROJECT=<project id to use> -o ZONE=<Google Cloud zone to create the VMs in>
devpod provider use gcloud
```

Alternatively, you can use the default gcloud provider and switch to this enhanced version later:

```sh
# Add the enhanced provider
devpod provider add gcloud --url https://github.com/badal-io/devpod-provider-gcloud
devpod provider use gcloud
```

Option `PROJECT` must be set when adding the provider
(unless the project to be used is set as the current project in `gcloud`).

Option `ZONE` should be set when adding the provider.

Options can be set using `devpod provider set-options`, for example:

```sh
devpod provider set-options gcloud -o DISK_IMAGE=my-custom-vm-image
```

Be aware that authentication is obtained using `gcloud` CLI tool, take a look
[here](https://developers.google.com/accounts/docs/application-default-credentials)
for more information.

### Creating your first devpod workspace with gcloud

After the initial setup, just use:

```sh
devpod up .
```

You'll need to wait for the machine and workspace setup.

### Using Identity-Aware Proxy (IAP) for SSH

This provider supports connecting to VMs without public IPs using Google Cloud's Identity-Aware Proxy. To use IAP:

```sh
devpod provider set-options gcloud -o PUBLIC_IP_ENABLED=false -o SUBNETWORK=<your-subnet>
```

**Requirements for IAP:**
- A subnetwork must be specified
- Cloud NAT must be configured for the subnet (required for outbound internet access)
- The provider will automatically:
  - Validate Cloud NAT configuration before creating the VM
  - Configure SSH with ProxyCommand for IAP tunneling
  - Create necessary user accounts and SSH keys

If Cloud NAT is not configured, the provider will display an error with exact `gcloud` commands to enable it.

### Customize the VM Instance

This provider has the following options:

| NAME                | REQUIRED | DESCRIPTION                                                    | DEFAULT                                              |
|---------------------|----------|----------------------------------------------------------------|------------------------------------------------------|
| DISK_IMAGE          | false    | The disk image to use.                                         | projects/cos-cloud/global/images/cos-101-17162-127-5 |
| DISK_SIZE           | false    | The disk size to use (GB).                                     | 40                                                   |
| MACHINE_TYPE        | false    | The machine type to use.                                       | c2-standard-4                                        |
| PROJECT             | true     | The project id to use.                                         |                                                      |
| ZONE                | true     | The google cloud zone to create the VM in. E.g. europe-west1-d |                                                      |
| NETWORK             | false    | The network id to use.                                         |                                                      |
| SUBNETWORK          | false    | The subnetwork id to use.                                      |                                                      |
| TAG                 | false    | A tag to attach to the instance.                               | devpod                                               |
| SERVICE_ACCOUNT     | false    | A service account to attach to instance.                       |                                                      |
| PUBLIC_IP_ENABLED   | false    | Use a public IP to access the instance (false = IAP mode).     | true                                                 |


