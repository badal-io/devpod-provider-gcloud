#!/bin/bash
# DevPod GCloud Provider - Timeout Fixes Test Execution Script
# Version: 1.0.0
# Usage: ./run-timeout-tests.sh [quick|full|debug]

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test results
TESTS_PASSED=0
TESTS_FAILED=0
TEST_RESULTS=()

# Configuration
TEST_MODE="${1:-quick}"
PROJECT="${GCLOUD_PROJECT:-}"
ZONE="${GCLOUD_ZONE:-us-central1-a}"
SUBNETWORK="${GCLOUD_SUBNETWORK:-}"
TEST_PREFIX="devpod-timeout-test"
LOG_DIR="./test-results-$(date +%Y%m%d-%H%M%S)"

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
    TEST_RESULTS+=("✓ $1")
}

log_error() {
    echo -e "${RED}[FAILED]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    TEST_RESULTS+=("✗ $1")
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

check_prerequisites() {
    log_info "Checking prerequisites..."

    if ! command -v gcloud &> /dev/null; then
        log_error "gcloud CLI not found. Please install: https://cloud.google.com/sdk/docs/install"
        exit 1
    fi

    if ! command -v devpod &> /dev/null; then
        log_error "devpod CLI not found. Please install: https://devpod.sh/docs/getting-started/install"
        exit 1
    fi

    if [ -z "$PROJECT" ]; then
        PROJECT=$(gcloud config get-value project 2>/dev/null)
        if [ -z "$PROJECT" ]; then
            log_error "GCP project not set. Set GCLOUD_PROJECT or run: gcloud config set project PROJECT_ID"
            exit 1
        fi
    fi

    log_success "Prerequisites check passed (Project: $PROJECT, Zone: $ZONE)"
}

test_cloud_nat_detection() {
    log_info "Test 1.1: Cloud NAT Detection"

    if [ -z "$SUBNETWORK" ]; then
        log_warning "GCLOUD_SUBNETWORK not set, skipping Cloud NAT test"
        return
    fi

    # Extract region from zone
    REGION="${ZONE%-*}"

    # Check if Cloud NAT exists
    NAT_EXISTS=$(gcloud compute routers nats list \
        --router-region=$REGION \
        --project=$PROJECT \
        --format="value(name)" 2>/dev/null | wc -l)

    if [ "$NAT_EXISTS" -gt 0 ]; then
        log_success "Cloud NAT is configured for region $REGION"
    else
        log_warning "Cloud NAT not found. DevPod should detect and report this."
    fi
}

test_iap_firewall_rules() {
    log_info "Test 1.3: IAP Firewall Rules"

    # Check for existing IAP firewall rules
    IAP_RULES=$(gcloud compute firewall-rules list \
        --project=$PROJECT \
        --filter="sourceRanges:35.235.240.0/20 AND allowed:tcp:22" \
        --format="value(name)" 2>/dev/null)

    if [ -n "$IAP_RULES" ]; then
        log_success "IAP firewall rules exist: $IAP_RULES"
    else
        log_info "No IAP firewall rules found. DevPod should auto-create them."
    fi
}

test_instance_creation() {
    local workspace_name="$1"
    log_info "Test 2.1: Instance Creation - $workspace_name"

    # Create workspace with debug output
    if devpod up "$workspace_name" \
        --provider gcloud \
        --debug 2>&1 | tee "$LOG_DIR/${workspace_name}-creation.log"; then

        log_success "Instance created: $workspace_name"

        # Verify instance exists
        if gcloud compute instances describe "$workspace_name" \
            --project=$PROJECT \
            --zone=$ZONE &>/dev/null; then
            log_success "Instance verified in GCP: $workspace_name"
        else
            log_error "Instance not found in GCP: $workspace_name"
        fi
    else
        log_error "Instance creation failed: $workspace_name"
        return 1
    fi
}

test_ssh_connection() {
    local workspace_name="$1"
    log_info "Test 3.1: SSH Connection - $workspace_name"

    # Test basic SSH command
    if devpod ssh "$workspace_name" --command "echo 'SSH connection successful'" 2>&1 | tee "$LOG_DIR/${workspace_name}-ssh.log"; then
        log_success "SSH connection works: $workspace_name"
    else
        log_error "SSH connection failed: $workspace_name"
        return 1
    fi
}

test_ssh_stability() {
    local workspace_name="$1"
    log_info "Test 3.2: SSH Connection Stability - $workspace_name"

    # Test long-running command (validate keepalive)
    log_info "Testing 60-second connection stability..."
    if devpod ssh "$workspace_name" --command "sleep 60 && echo 'Connection stable for 60 seconds'" 2>&1 | tee "$LOG_DIR/${workspace_name}-stability.log"; then
        log_success "SSH connection stable for 60 seconds: $workspace_name"
    else
        log_error "SSH connection dropped during 60-second test: $workspace_name"
        return 1
    fi
}

test_agent_injection() {
    local workspace_name="$1"
    log_info "Test 4.1: Agent Injection - $workspace_name"

    # Verify agent exists on instance
    if devpod ssh "$workspace_name" --command "ls -la /var/lib/toolbox/devpod/" 2>&1 | tee "$LOG_DIR/${workspace_name}-agent.log"; then
        log_success "DevPod agent injected successfully: $workspace_name"
    else
        log_error "DevPod agent not found: $workspace_name"
        return 1
    fi
}

test_workspace_operations() {
    local workspace_name="$1"
    log_info "Test 5.2: Workspace Operations - $workspace_name"

    # Test multiple commands
    log_info "Testing pwd command..."
    devpod ssh "$workspace_name" --command "pwd"

    log_info "Testing whoami command..."
    devpod ssh "$workspace_name" --command "whoami"

    log_info "Testing environment variables..."
    devpod ssh "$workspace_name" --command "env | grep DEVPOD || true"

    log_success "Workspace operations completed: $workspace_name"
}

test_file_operations() {
    local workspace_name="$1"
    log_info "Test 5.3: File Operations - $workspace_name"

    # Create test file
    echo "DevPod timeout test - $(date)" > "$LOG_DIR/test-upload.txt"

    # Upload file
    if devpod ssh "$workspace_name" --command "cat > /tmp/test-upload.txt" < "$LOG_DIR/test-upload.txt"; then
        log_info "File uploaded successfully"
    else
        log_error "File upload failed"
        return 1
    fi

    # Verify file content
    if devpod ssh "$workspace_name" --command "cat /tmp/test-upload.txt" > "$LOG_DIR/test-download.txt"; then
        if diff "$LOG_DIR/test-upload.txt" "$LOG_DIR/test-download.txt"; then
            log_success "File operations work correctly: $workspace_name"
        else
            log_error "File content mismatch: $workspace_name"
            return 1
        fi
    else
        log_error "File download failed: $workspace_name"
        return 1
    fi
}

test_connection_timing() {
    local workspace_name="$1"
    log_info "Test Performance: Connection Timing - $workspace_name"

    # Measure SSH command latency
    local start=$(date +%s)
    devpod ssh "$workspace_name" --command "echo 'timing test'" &>/dev/null
    local end=$(date +%s)
    local duration=$((end - start))

    if [ $duration -lt 5 ]; then
        log_success "SSH command latency: ${duration}s (target: <5s)"
    else
        log_warning "SSH command latency: ${duration}s (slower than target <5s)"
    fi
}

cleanup_workspace() {
    local workspace_name="$1"
    log_info "Cleaning up workspace: $workspace_name"

    if devpod delete "$workspace_name" --force 2>&1 | tee "$LOG_DIR/${workspace_name}-cleanup.log"; then
        log_success "Workspace deleted: $workspace_name"
    else
        log_warning "Workspace deletion had issues: $workspace_name"
    fi
}

print_summary() {
    echo ""
    echo "=================================="
    echo "Test Execution Summary"
    echo "=================================="
    echo "Test Mode: $TEST_MODE"
    echo "Project: $PROJECT"
    echo "Zone: $ZONE"
    echo "Tests Passed: $TESTS_PASSED"
    echo "Tests Failed: $TESTS_FAILED"
    echo ""
    echo "Detailed Results:"
    for result in "${TEST_RESULTS[@]}"; do
        echo "  $result"
    done
    echo ""
    echo "Logs saved to: $LOG_DIR"
    echo "=================================="

    if [ $TESTS_FAILED -gt 0 ]; then
        echo -e "${RED}OVERALL RESULT: FAILED${NC}"
        exit 1
    else
        echo -e "${GREEN}OVERALL RESULT: PASSED${NC}"
        exit 0
    fi
}

# Main test execution
main() {
    echo "=================================="
    echo "DevPod Timeout Fixes Test Suite"
    echo "=================================="
    echo "Mode: $TEST_MODE"
    echo "Project: $PROJECT"
    echo "Zone: $ZONE"
    echo "=================================="
    echo ""

    # Create log directory
    mkdir -p "$LOG_DIR"

    # Run tests
    check_prerequisites

    # Phase 1: Pre-flight
    echo -e "\n${BLUE}=== Phase 1: Pre-flight Validation ===${NC}"
    test_cloud_nat_detection
    test_iap_firewall_rules

    # Generate unique workspace name
    WORKSPACE_NAME="${TEST_PREFIX}-$(date +%s)"

    # Phase 2-5: Full lifecycle test
    echo -e "\n${BLUE}=== Phase 2: Instance Creation ===${NC}"
    if ! test_instance_creation "$WORKSPACE_NAME"; then
        log_error "Instance creation failed. Stopping tests."
        print_summary
        exit 1
    fi

    echo -e "\n${BLUE}=== Phase 3: SSH/IAP Connection ===${NC}"
    test_ssh_connection "$WORKSPACE_NAME"

    if [ "$TEST_MODE" = "full" ] || [ "$TEST_MODE" = "debug" ]; then
        test_ssh_stability "$WORKSPACE_NAME"
    fi

    echo -e "\n${BLUE}=== Phase 4: Agent Injection ===${NC}"
    test_agent_injection "$WORKSPACE_NAME"

    echo -e "\n${BLUE}=== Phase 5: Workspace Operations ===${NC}"
    test_workspace_operations "$WORKSPACE_NAME"

    if [ "$TEST_MODE" = "full" ] || [ "$TEST_MODE" = "debug" ]; then
        test_file_operations "$WORKSPACE_NAME"
        test_connection_timing "$WORKSPACE_NAME"
    fi

    # Cleanup
    echo -e "\n${BLUE}=== Cleanup ===${NC}"
    cleanup_workspace "$WORKSPACE_NAME"

    # Print summary
    print_summary
}

# Run main
main
