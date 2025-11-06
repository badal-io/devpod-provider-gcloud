# DevPod GCloud Provider - Timeout Fixes Test Plan

**Version:** 1.0.0
**Date:** 2025-11-06
**Tester:** Hive Mind QA Agent
**Focus:** IAP SSH Connection Timeout Fixes

---

## Executive Summary

This test plan validates critical timeout fixes for DevPod GCloud provider focusing on IAP SSH connectivity, agent injection, and workspace creation reliability. The fixes address timeout issues when connecting to private IP instances via Identity-Aware Proxy (IAP).

### Critical Fixes Under Test

1. **IAP SSH Tunnel Timeouts** (commit 099f7f7)
   - `ConnectTimeout: 60` seconds (was immediate failure)
   - `ServerAliveInterval: 30` seconds
   - `ServerAliveCountMax: 10` (5-minute keepalive window)
   - `TCPKeepAlive: yes`

2. **Devpod User Creation** (commit e52fc19)
   - Startup script automatically creates `devpod` user for IAP connections
   - Extracts SSH keys from metadata
   - Configures sudo access
   - Waits for startup script completion (30s + verification)

3. **IAP Firewall Auto-Creation** (commit 30a7167)
   - Automatic detection of missing IAP firewall rules
   - Auto-creation of `devpod-allow-iap` rule
   - Source range: `35.235.240.0/20` (Google IAP forwarding)

4. **Instance Readiness Wait** (commit 099f7f7)
   - Wait for RUNNING status (up to 5 minutes)
   - Wait for startup script completion (30 seconds)
   - SSH readiness verification (6 attempts, 10s timeout each)

5. **Cloud NAT Detection** (commit 9f632d4)
   - Pre-flight check for Cloud NAT configuration
   - Clear error messages with setup commands
   - Region/subnet validation

---

## Test Environment Requirements

### Prerequisites
- GCP project with billing enabled
- `gcloud` CLI installed and authenticated
- DevPod CLI installed
- Network configuration:
  - VPC network (default or custom)
  - Subnetwork with private Google access enabled
  - Cloud NAT configured for the region
  - IAP API enabled

### Test Configurations

#### Configuration A: Private IP with Cloud NAT (PRIMARY TEST)
```bash
export PUBLIC_IP_ENABLED=false
export SUBNETWORK=my-subnet
export NETWORK=my-vpc
export TAG=devpod-test
```

#### Configuration B: Public IP (BASELINE)
```bash
export PUBLIC_IP_ENABLED=true
```

---

## Test Phases

### Phase 1: Pre-flight Validation (5 minutes)

**Objective:** Verify environment setup and provider detection capabilities

#### Test 1.1: Cloud NAT Detection - Positive Case
```bash
# Given: Cloud NAT is configured for the subnet
# When: Creating instance with PUBLIC_IP_ENABLED=false
devpod provider use gcloud
devpod up test-workspace-nat --provider gcloud --debug

# Expected:
# ✓ No Cloud NAT error
# ✓ Provider proceeds to instance creation
# ✓ Log shows "Cloud NAT is configured"
```

**Pass Criteria:**
- No Cloud NAT configuration errors
- Instance creation starts successfully
- Debug logs confirm NAT detection

#### Test 1.2: Cloud NAT Detection - Negative Case
```bash
# Given: Cloud NAT is NOT configured
# When: Creating instance with PUBLIC_IP_ENABLED=false
devpod up test-workspace-no-nat --provider gcloud

# Expected:
# ✗ Clear error message about missing Cloud NAT
# ✓ Error includes gcloud commands to configure NAT
# ✓ Error mentions devpod-nat-router and devpod-nat-config
```

**Pass Criteria:**
- Error is caught before instance creation
- Error message includes specific gcloud commands
- Error references correct region and subnet

#### Test 1.3: IAP Firewall Detection
```bash
# Given: IAP firewall rules don't exist
# When: Creating instance with PUBLIC_IP_ENABLED=false
devpod up test-workspace-iap --provider gcloud --debug

# Expected:
# ✓ Log shows "Checking IAP firewall configuration..."
# ✓ Log shows "IAP firewall rule not found, creating automatically..."
# ✓ Log shows "Successfully created IAP firewall rule 'devpod-allow-iap'"
# ✓ Firewall rule exists with correct configuration
```

**Validation Commands:**
```bash
# Verify firewall rule
gcloud compute firewall-rules describe devpod-allow-iap \
  --project=$PROJECT --format=yaml

# Expected fields:
# - sourceRanges: 35.235.240.0/20
# - allowed: tcp:22
# - direction: INGRESS
# - targetTags: devpod-test (if TAG specified)
```

---

### Phase 2: Instance Creation Tests (10 minutes)

**Objective:** Validate instance creation with startup script and SSH configuration

#### Test 2.1: Instance Creation with Startup Script
```bash
# Create instance
devpod up test-workspace-startup --provider gcloud --debug

# Expected:
# ✓ Instance created successfully
# ✓ Startup script attached to instance metadata
# ✓ Log shows "Waiting for instance to be fully ready..."
# ✓ Log shows "Instance is running, waiting for startup script to complete..."
```

**Validation Commands:**
```bash
# Check instance metadata
gcloud compute instances describe test-workspace-startup \
  --zone=$ZONE --format="value(metadata.items[startup-script])"

# Expected startup script content:
# - useradd -m -s /bin/bash devpod
# - usermod -aG sudo devpod
# - echo "devpod ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/devpod
# - SSH key extraction from metadata
# - authorized_keys setup
```

#### Test 2.2: SSH Config File Creation
```bash
# After instance creation, check SSH config
MACHINE_FOLDER="$HOME/.devpod/providers/gcloud/workspaces/test-workspace-startup"
cat "$MACHINE_FOLDER/ssh_config"

# Expected content:
# Host test-workspace-startup
#     HostName test-workspace-startup
#     User devpod
#     IdentityFile $MACHINE_FOLDER/id_devpod_rsa
#     StrictHostKeyChecking no
#     UserKnownHostsFile /dev/null
#     ProxyCommand gcloud compute start-iap-tunnel %h %p --listen-on-stdin --project=$PROJECT --zone=$ZONE --verbosity=warning
#     ConnectTimeout 60
#     ServerAliveInterval 30
#     ServerAliveCountMax 10
#     TCPKeepAlive yes
```

**Pass Criteria:**
- SSH config file exists and has correct permissions (0600)
- ProxyCommand includes correct project and zone
- Timeout values match expected: 60s connect, 30s keepalive
- IdentityFile path is correct

---

### Phase 3: SSH/IAP Connection Tests (15 minutes)

**Objective:** Validate SSH connectivity through IAP tunnel with proper timeouts

#### Test 3.1: Initial SSH Connection
```bash
# Given: Instance just created, startup script still running
# When: DevPod attempts first SSH connection
# Expected: Connection succeeds within 60 seconds

# Monitor connection in debug mode
devpod up test-workspace-connect --provider gcloud --debug 2>&1 | tee test-connection.log

# Expected log entries:
# "Waiting for SSH to be ready (attempt 1/6)..."
# "Waiting for SSH to be ready (attempt 2/6)..."
# "Instance is fully ready for SSH connections"
```

**Validation Points:**
- Connection succeeds within 6 attempts (60 seconds total)
- No "Connection timeout" errors
- IAP tunnel establishes successfully

#### Test 3.2: SSH Connection Stability
```bash
# Test SSH connection directly using generated config
MACHINE_FOLDER="$HOME/.devpod/providers/gcloud/workspaces/test-workspace-connect"

# Test 1: Simple command
ssh -F "$MACHINE_FOLDER/ssh_config" test-workspace-connect "echo 'Hello from VM'"

# Test 2: Long-running command (validate keepalive)
ssh -F "$MACHINE_FOLDER/ssh_config" test-workspace-connect "sleep 120 && echo 'Still connected'"

# Test 3: Multiple rapid connections
for i in {1..5}; do
  ssh -F "$MACHINE_FOLDER/ssh_config" test-workspace-connect "date"
done
```

**Pass Criteria:**
- All commands execute successfully
- No connection drops during 2-minute sleep
- Rapid connections don't cause failures
- No "Connection reset" or "Broken pipe" errors

#### Test 3.3: Timeout Handling - Slow Startup
```bash
# Simulate slow startup by attempting connection immediately after creation
# (before startup script completes)

# Create instance but don't wait for DevPod's readiness check
gcloud compute instances create test-slow-startup \
  --zone=$ZONE --project=$PROJECT \
  --machine-type=e2-medium \
  --metadata-from-file=startup-script=startup.sh

# Attempt SSH connection immediately
ssh -F ssh_config test-slow-startup "whoami"

# Expected:
# - First few attempts timeout (startup script still running)
# - Connection succeeds within 60 seconds after retries
# - No premature failures
```

---

### Phase 4: Agent Injection Tests (10 minutes)

**Objective:** Validate DevPod agent download and injection over IAP

#### Test 4.1: Agent Download via IAP
```bash
# Create workspace (triggers agent injection)
devpod up test-workspace-agent --provider gcloud --debug

# Expected:
# ✓ SSH connection established via IAP
# ✓ Agent binary downloaded to instance
# ✓ Agent installed at /var/lib/toolbox/devpod
# ✓ Agent starts successfully
```

**Validation Commands:**
```bash
# SSH into instance and verify agent
ssh -F $MACHINE_FOLDER/ssh_config test-workspace-agent "ls -la /var/lib/toolbox/devpod/"

# Expected:
# - devpod agent binary exists
# - Correct permissions (executable)
# - Size > 0 bytes
```

#### Test 4.2: Agent Communication via IAP
```bash
# Test agent command execution
devpod ssh test-workspace-agent --command "pwd"
devpod ssh test-workspace-agent --command "whoami"
devpod ssh test-workspace-agent --command "env | grep DEVPOD"

# Expected:
# ✓ All commands execute successfully
# ✓ Output is captured correctly
# ✓ No connection errors
```

---

### Phase 5: End-to-End Workspace Tests (15 minutes)

**Objective:** Full DevPod workspace lifecycle via IAP

#### Test 5.1: Complete Workspace Creation
```bash
# Create workspace from Git repository
devpod up test-workspace-e2e \
  --provider gcloud \
  --source https://github.com/loft-sh/devpod-example-simple \
  --debug

# Expected workflow:
# 1. Instance creation with startup script
# 2. SSH config with IAP ProxyCommand
# 3. Wait for instance readiness (30s + verification)
# 4. SSH connection via IAP
# 5. Agent injection over IAP
# 6. Git repository clone
# 7. DevContainer setup
# 8. IDE connection ready
```

**Pass Criteria:**
- Entire workflow completes without timeout errors
- Total time < 10 minutes
- Workspace is accessible via IDE
- No "Connection timeout" or "Agent injection failed" errors

#### Test 5.2: Workspace Operations
```bash
# Test various workspace operations
devpod ssh test-workspace-e2e --command "ls -la"
devpod ssh test-workspace-e2e --command "docker ps"

# Stop workspace
devpod stop test-workspace-e2e

# Start workspace again (test reconnection)
devpod up test-workspace-e2e

# Expected:
# ✓ All operations succeed
# ✓ Reconnection works without timeout
# ✓ SSH tunnel re-establishes correctly
```

#### Test 5.3: File Operations over IAP
```bash
# Test file transfer via SSH
echo "test content" > test-file.txt
devpod ssh test-workspace-e2e --command "cat > /tmp/test-upload.txt" < test-file.txt

# Verify file
devpod ssh test-workspace-e2e --command "cat /tmp/test-upload.txt"

# Download file
devpod ssh test-workspace-e2e --command "cat /tmp/test-upload.txt" > test-download.txt
diff test-file.txt test-download.txt

# Expected:
# ✓ File uploads successfully
# ✓ File contents are correct
# ✓ Download works without corruption
```

---

### Phase 6: Error Recovery Tests (10 minutes)

**Objective:** Validate timeout handling and retry logic

#### Test 6.1: Connection Retry Logic
```bash
# Create instance and immediately try to connect
# (simulates connection during startup script execution)

devpod up test-workspace-retry --provider gcloud --debug 2>&1 | tee retry-test.log

# Analyze log for retry attempts
grep "Waiting for SSH to be ready" retry-test.log | wc -l

# Expected:
# - Multiple retry attempts visible in log
# - Connection succeeds within 6 attempts
# - No premature failures
# - Clear progress messages
```

#### Test 6.2: Network Interruption Recovery
```bash
# Create workspace
devpod up test-workspace-recovery --provider gcloud

# Simulate network interruption (suspend/resume IAP tunnel)
# This tests ServerAliveInterval and ServerAliveCountMax

# Start long-running command
devpod ssh test-workspace-recovery --command "sleep 300" &
SLEEP_PID=$!

# Wait 60 seconds, then check if connection is still alive
sleep 60
ps -p $SLEEP_PID

# Expected:
# - Command still running after 60 seconds
# - SSH connection maintained via keepalive packets
# - No disconnection during idle period
```

#### Test 6.3: Graceful Failure Messages
```bash
# Test with missing IAP permissions
gcloud projects remove-iam-policy-binding $PROJECT \
  --member=user:$USER \
  --role=roles/iap.tunnelResourceAccessor

devpod up test-workspace-noiam --provider gcloud 2>&1 | tee error-test.log

# Expected error message content:
# - Clear indication of IAP permission issue
# - Suggestion to add IAP tunnel role
# - gcloud command to fix permissions

# Restore permissions
gcloud projects add-iam-policy-binding $PROJECT \
  --member=user:$USER \
  --role=roles/iap.tunnelResourceAccessor
```

---

## Performance Benchmarks

### Target Metrics

| Metric | Target | Critical Threshold |
|--------|--------|-------------------|
| Instance creation | < 3 minutes | < 5 minutes |
| First SSH connection | < 60 seconds | < 90 seconds |
| Agent injection | < 2 minutes | < 4 minutes |
| Total workspace creation | < 8 minutes | < 12 minutes |
| SSH command latency | < 2 seconds | < 5 seconds |
| Connection stability | 100% | > 95% |

### Timeout Values Verification

| Configuration | Value | Purpose |
|--------------|-------|---------|
| ConnectTimeout | 60s | Initial SSH connection |
| ServerAliveInterval | 30s | Keepalive packet frequency |
| ServerAliveCountMax | 10 | Max missed keepalives (5 min total) |
| Startup script wait | 30s | User creation completion |
| SSH readiness checks | 6 × 10s | Post-startup verification |
| Instance status polling | 60 × 5s | Wait for RUNNING state |

---

## Test Execution Commands

### Quick Test Suite
```bash
#!/bin/bash
# Run all critical tests

export PROJECT="your-gcp-project"
export ZONE="us-central1-a"
export SUBNETWORK="your-subnet"
export PUBLIC_IP_ENABLED=false

# Test 1: Cloud NAT detection
echo "Test 1: Cloud NAT detection"
devpod up test-nat --provider gcloud --debug || echo "FAILED: Cloud NAT"

# Test 2: IAP firewall auto-creation
echo "Test 2: IAP firewall"
gcloud compute firewall-rules describe devpod-allow-iap --project=$PROJECT

# Test 3: Instance creation with startup script
echo "Test 3: Instance creation"
devpod up test-startup --provider gcloud --debug || echo "FAILED: Startup"

# Test 4: SSH connection via IAP
echo "Test 4: SSH connection"
devpod ssh test-startup --command "whoami" || echo "FAILED: SSH"

# Test 5: Agent injection
echo "Test 5: Agent injection"
devpod ssh test-startup --command "ls /var/lib/toolbox/devpod/" || echo "FAILED: Agent"

# Test 6: Workspace operations
echo "Test 6: Workspace operations"
devpod up test-e2e --source https://github.com/loft-sh/devpod-example-simple --provider gcloud

# Cleanup
devpod delete test-nat test-startup test-e2e --force
```

### Detailed Debug Test
```bash
#!/bin/bash
# Detailed test with full logging

export DEVPOD_DEBUG=true
export PROJECT="your-gcp-project"
export ZONE="us-central1-a"

# Create workspace with full debug output
devpod up test-debug \
  --provider gcloud \
  --debug \
  2>&1 | tee devpod-debug.log

# Analyze log for timeout-related entries
echo "=== Timeout Analysis ==="
grep -i "timeout\|waiting\|retry\|attempt" devpod-debug.log

# Analyze SSH connection establishment
echo "=== SSH Connection Analysis ==="
grep -i "ssh\|connect\|iap\|tunnel" devpod-debug.log

# Analyze agent injection
echo "=== Agent Injection Analysis ==="
grep -i "agent\|inject\|download" devpod-debug.log
```

---

## Validation Checklist

### Critical Success Criteria

- [ ] Cloud NAT detection works correctly (positive and negative cases)
- [ ] IAP firewall rules auto-created successfully
- [ ] Startup script creates devpod user within 30 seconds
- [ ] SSH config generated with correct IAP ProxyCommand
- [ ] SSH connection establishes within 60 seconds
- [ ] SSH connection remains stable for 5+ minutes (keepalive)
- [ ] DevPod agent downloads successfully over IAP
- [ ] Agent commands execute without timeout
- [ ] Complete workspace creation succeeds end-to-end
- [ ] File transfers work correctly over IAP
- [ ] Retry logic handles slow startups gracefully
- [ ] Error messages are clear and actionable

### Performance Criteria

- [ ] Instance creation < 5 minutes
- [ ] First SSH connection < 90 seconds
- [ ] Total workspace setup < 12 minutes
- [ ] SSH command latency < 5 seconds
- [ ] Connection stability > 95%

### Code Quality Criteria

- [ ] No hardcoded timeouts that could cause premature failures
- [ ] All timeout values are configurable or well-justified
- [ ] Error messages include specific troubleshooting steps
- [ ] Logging provides clear visibility into connection process
- [ ] Retry logic is robust but not infinite

---

## Known Issues and Limitations

### Current Limitations

1. **Startup Script Timing**: Fixed 30-second wait may not be sufficient for slower instances
   - Mitigation: 6 additional SSH verification attempts (60 more seconds)

2. **IAP Tunnel Instability**: Google IAP can occasionally drop tunnels
   - Mitigation: ServerAliveInterval keepalive packets every 30 seconds

3. **First Connection Delay**: IAP tunnel establishment takes 10-20 seconds
   - Expected: Normal IAP behavior, mitigated by 60s ConnectTimeout

### Edge Cases to Monitor

1. Instances with very slow startup scripts (> 90 seconds)
2. Networks with high latency to Google IAP endpoints
3. Concurrent workspace creations (IAP tunnel contention)
4. Instances in regions with IAP service issues

---

## Test Results Template

### Test Execution Summary

```
Date: [DATE]
Tester: [NAME]
Environment: [PROJECT/ZONE]
Provider Version: [VERSION]
DevPod Version: [VERSION]

Phase 1 - Pre-flight: [PASS/FAIL]
  Test 1.1 Cloud NAT Detection: [PASS/FAIL]
  Test 1.2 Cloud NAT Error: [PASS/FAIL]
  Test 1.3 IAP Firewall: [PASS/FAIL]

Phase 2 - Instance Creation: [PASS/FAIL]
  Test 2.1 Startup Script: [PASS/FAIL]
  Test 2.2 SSH Config: [PASS/FAIL]

Phase 3 - SSH/IAP Connection: [PASS/FAIL]
  Test 3.1 Initial Connection: [PASS/FAIL]
  Test 3.2 Connection Stability: [PASS/FAIL]
  Test 3.3 Timeout Handling: [PASS/FAIL]

Phase 4 - Agent Injection: [PASS/FAIL]
  Test 4.1 Agent Download: [PASS/FAIL]
  Test 4.2 Agent Communication: [PASS/FAIL]

Phase 5 - E2E Workspace: [PASS/FAIL]
  Test 5.1 Complete Creation: [PASS/FAIL]
  Test 5.2 Workspace Operations: [PASS/FAIL]
  Test 5.3 File Operations: [PASS/FAIL]

Phase 6 - Error Recovery: [PASS/FAIL]
  Test 6.1 Retry Logic: [PASS/FAIL]
  Test 6.2 Network Recovery: [PASS/FAIL]
  Test 6.3 Error Messages: [PASS/FAIL]

Performance Benchmarks:
  Instance creation: [TIME]
  First SSH connection: [TIME]
  Agent injection: [TIME]
  Total workspace creation: [TIME]

Overall Result: [PASS/FAIL]
Critical Issues: [LIST]
Recommendations: [LIST]
```

---

## Next Steps

### On Test Pass
1. Update version to 1.0.0 (stable)
2. Update CHANGELOG.md with fix details
3. Create GitHub release with test results
4. Update documentation with IAP requirements
5. Notify community of stable release

### On Test Failure
1. Document specific failure modes
2. Analyze timeout values and adjust if needed
3. Enhance error messages based on failure patterns
4. Consider additional retry logic
5. Re-test with adjusted parameters

---

## Appendix: Manual Test Commands

### Verify IAP Tunnel Directly
```bash
# Test IAP tunnel without DevPod
gcloud compute start-iap-tunnel INSTANCE_NAME 22 \
  --local-host-port=localhost:2222 \
  --project=$PROJECT \
  --zone=$ZONE &

# Wait for tunnel
sleep 5

# Test SSH through tunnel
ssh -i ~/.ssh/id_rsa -p 2222 devpod@localhost "echo 'IAP tunnel works'"
```

### Verify Startup Script Execution
```bash
# Check serial console output
gcloud compute instances get-serial-port-output INSTANCE_NAME \
  --project=$PROJECT \
  --zone=$ZONE | grep "devpod"

# Expected output:
# - "useradd -m -s /bin/bash devpod"
# - "usermod -aG sudo devpod"
```

### Verify SSH Keys
```bash
# SSH into instance and check authorized_keys
gcloud compute ssh INSTANCE_NAME \
  --project=$PROJECT \
  --zone=$ZONE \
  --tunnel-through-iap \
  --command "sudo cat /home/devpod/.ssh/authorized_keys"
```

---

**Test Plan Version:** 1.0.0
**Document Status:** Ready for Execution
**Estimated Execution Time:** 45-60 minutes
**Risk Level:** High (Core SSH Connectivity)
