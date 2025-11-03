# IAP SSH Connection Solution - DevPod GCP Provider

**Repository**: https://github.com/badal-io/devpod-provider-gcloud

## ✅ Solution Implemented

We've successfully implemented Identity-Aware Proxy (IAP) SSH support for the DevPod GCP provider using SSH ProxyCommand with `gcloud compute start-iap-tunnel` for persistent connections.

## 🎯 What Was Changed

### Implementation Approach: SSH ProxyCommand

After testing various approaches, we implemented IAP support using SSH ProxyCommand, which provides:
- **Persistent SSH sessions** (required for DevPod agent injection)
- **Clean stdout/stderr** (no tunnel startup messages)
- **Automatic tunnel lifecycle management** by SSH client

### Files Modified:

#### 1. `cmd/create.go` - VM Creation & SSH Configuration
- Added `configureSSHForIAP()` function to create SSH config with ProxyCommand
- Added startup script to create devpod user and populate authorized_keys
- Added Cloud NAT validation to ensure internet access for private VMs

#### 2. `pkg/gcloud/gcloud.go` - Cloud NAT Detection
- Added `CheckCloudNAT()` method to validate network configuration
- Prevents workspace creation failures due to missing internet access

### SSH Configuration (ProxyCommand)

```
Host MACHINE_ID
    HostName MACHINE_ID
    User devpod
    IdentityFile /path/to/id_devpod_rsa
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    ProxyCommand gcloud compute start-iap-tunnel %h %p --listen-on-stdin --project=PROJECT --zone=ZONE --verbosity=warning
    ServerAliveInterval 30
    ServerAliveCountMax 3
```

This configuration allows SSH to manage the IAP tunnel automatically, providing persistent connections required by DevPod.

## ✅ Verification - IAP Works!

Testing confirmed full IAP functionality with:
- ✅ SSH connections through ProxyCommand work correctly
- ✅ DevPod agent injection succeeds over persistent SSH sessions
- ✅ Repository cloning works through the tunnel
- ✅ Docker container builds complete successfully
- ✅ `devpod status` and `devpod ssh` commands function properly

### Cloud NAT Validation

The provider automatically validates Cloud NAT configuration before creating VMs. If Cloud NAT is not configured, you'll receive a helpful error message with exact `gcloud` commands to enable it:

```
Cloud NAT is not configured for subnet 'your-subnet' in region 'us-central1'.

DevPod instances without public IPs require Cloud NAT for outbound internet access
to download the DevPod agent and dependencies.

To enable Cloud NAT, run the following commands:
  [exact gcloud commands with your project/region/network values]
```

## 📋 Prerequisites

For IAP to work, your GCP project must have:

### 1. Cloud NAT Configuration
**Required for internet access:**
```bash
# Create a Cloud Router (if one doesn't exist)
gcloud compute routers create devpod-nat-router \
  --project=YOUR_PROJECT \
  --region=YOUR_REGION \
  --network=YOUR_NETWORK

# Create Cloud NAT configuration
gcloud compute routers nats create devpod-nat-config \
  --router=devpod-nat-router \
  --region=YOUR_REGION \
  --nat-all-subnet-ip-ranges \
  --auto-allocate-nat-external-ips \
  --project=YOUR_PROJECT
```

The provider will automatically check for Cloud NAT and provide these commands if it's missing.

### 2. Firewall Rule
```bash
gcloud compute firewall-rules create allow-iap-ssh \
  --direction=INGRESS \
  --action=allow \
  --rules=tcp:22 \
  --source-ranges=35.235.240.0/20
```

### 3. IAM Permissions
User/service account needs:
- `roles/iap.tunnelResourceAccessor` (IAP-Secured Tunnel User)
- `roles/compute.instanceAdmin` or equivalent

### 4. IAP API
Ensure the IAP API is enabled in your project

## 🚀 Usage

### 1. Set Provider Option
```bash
devpod provider set-options gcloud -o PUBLIC_IP_ENABLED=false
```

### 2. Create Workspace
```bash
devpod up https://github.com/your/repo --provider gcloud
```

VMs will be created **without public IPs** and DevPod will automatically use IAP tunneling for all SSH connections.

## 🔍 How It Works

1. **VM Creation**: When `PUBLIC_IP_ENABLED=false`, VMs are created without external IP addresses
2. **SSH Connection**: DevPod invokes `gcloud compute ssh --tunnel-through-iap`
3. **IAP Tunnel**: Google Cloud establishes an encrypted HTTPS tunnel through IAP
4. **SSH Traffic**: SSH traffic flows through the IAP tunnel to the VM's internal IP

## 📊 Benefits Over Manual Approach

| Feature | Manual Tunnel | `--tunnel-through-iap` |
|---------|--------------|----------------------|
| Code Complexity | ~60 lines | ~20 lines |
| Port Management | Required | Automatic |
| Process Cleanup | Manual | Automatic |
| Error Messages | Generic | Detailed |
| Timeout Handling | Manual polling | Built-in |
| SSH Key Management | Manual | Integrated |

## 🐛 Troubleshooting

### IAP Permission Denied
```bash
# Grant IAP tunnel access
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member=user:EMAIL \
  --role=roles/iap.tunnelResourceAccessor
```

### Firewall Not Configured
```bash
# Check firewall rules
gcloud compute firewall-rules list --filter="sourceRanges:35.235.240.0/20"
```

### IAP API Not Enabled
```bash
# Enable IAP API
gcloud services enable iap.googleapis.com
```

## 📚 References

- **Repository**: https://github.com/badal-io/devpod-provider-gcloud
- [GitHub Issue #847](https://github.com/loft-sh/devpod/issues/847) - Original feature request (upstream)
- [GCP IAP Documentation](https://cloud.google.com/iap/docs/using-tcp-forwarding)
- [Connect to Linux VMs using IAP](https://cloud.google.com/compute/docs/connect/ssh-using-iap)
- [Cloud NAT Documentation](https://cloud.google.com/nat/docs/overview)

## 🎉 Result

IAP SSH connections now work seamlessly with DevPod GCP provider. VMs can be deployed without public IPs while maintaining full SSH connectivity through Google's Identity-Aware Proxy.
