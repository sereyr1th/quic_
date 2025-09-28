#!/bin/bash

# QUIC Connection Migration Test Script
# Tests connection migration capabilities with Caddy + QUIC backends

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
DOMAIN="${1:-localhost:8443}"
TEST_DURATION=60
CONCURRENT_CONNECTIONS=10
MIGRATION_INTERVAL=15

print_banner() {
    echo -e "${BLUE}"
    echo "╔══════════════════════════════════════════════╗"
    echo "║        QUIC Connection Migration Test        ║"
    echo "║          Caddy + QUIC Load Balancer         ║"
    echo "╚══════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_status() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Check dependencies
check_dependencies() {
    print_info "Checking dependencies..."
    
    if ! command -v curl >/dev/null 2>&1; then
        print_error "curl is required but not installed"
        exit 1
    fi
    
    if ! command -v jq >/dev/null 2>&1; then
        print_warning "jq not found, installing..."
        if command -v apt >/dev/null 2>&1; then
            sudo apt update && sudo apt install -y jq
        elif command -v yum >/dev/null 2>&1; then
            sudo yum install -y jq
        else
            print_error "Please install jq manually"
            exit 1
        fi
    fi
    
    # Check if curl supports HTTP/3
    if ! curl --help | grep -q "http3"; then
        print_warning "curl doesn't support HTTP/3, falling back to HTTP/2"
        HTTP3_SUPPORT=false
    else
        HTTP3_SUPPORT=true
        print_status "HTTP/3 support detected"
    fi
    
    print_status "Dependencies checked"
}

# Test basic connectivity
test_connectivity() {
    print_info "Testing basic connectivity to $DOMAIN..."
    
    if $HTTP3_SUPPORT; then
        if curl --http3 --connect-timeout 10 -s "https://$DOMAIN/health" >/dev/null 2>&1; then
            print_status "HTTP/3 connectivity successful"
            PROTOCOL="--http3"
        else
            print_warning "HTTP/3 failed, trying HTTP/2"
            PROTOCOL="--http2"
        fi
    else
        PROTOCOL="--http2"
    fi
    
    if curl $PROTOCOL --connect-timeout 10 -s "https://$DOMAIN/health" >/dev/null 2>&1; then
        print_status "Basic connectivity successful"
    else
        print_error "Cannot connect to $DOMAIN"
        exit 1
    fi
}

# Test load balancing
test_load_balancing() {
    print_info "Testing load balancing across backend instances..."
    
    local instances=()
    for i in {1..20}; do
        response=$(curl $PROTOCOL -s --connect-timeout 5 "https://$DOMAIN/api/status" 2>/dev/null)
        if [ $? -eq 0 ] && [ -n "$response" ]; then
            instance_id=$(echo "$response" | jq -r '.instance_id // .server_id // "unknown"' 2>/dev/null || echo "unknown")
            instances+=("$instance_id")
        fi
        sleep 0.1
    done
    
    # Count unique instances
    unique_instances=$(printf '%s\n' "${instances[@]}" | sort -u | wc -l)
    
    if [ "$unique_instances" -gt 1 ]; then
        print_status "Load balancing working: $unique_instances backend instances detected"
        printf '%s\n' "${instances[@]}" | sort | uniq -c
    else
        print_warning "Only 1 backend instance detected, load balancing may not be configured"
    fi
}

# Start long-running connections
start_long_connections() {
    print_info "Starting $CONCURRENT_CONNECTIONS long-running connections..."
    
    mkdir -p /tmp/quic-migration-test
    
    for i in $(seq 1 $CONCURRENT_CONNECTIONS); do
        {
            start_time=$(date +%s)
            connection_log="/tmp/quic-migration-test/connection_$i.log"
            
            # Start streaming connection
            timeout $TEST_DURATION curl $PROTOCOL \
                --connect-timeout 10 \
                --max-time $TEST_DURATION \
                -H "Connection: keep-alive" \
                -H "Cache-Control: no-cache" \
                "https://$DOMAIN/stream?duration=$TEST_DURATION&id=$i" \
                > "$connection_log" 2>&1 &
            
            echo $! > "/tmp/quic-migration-test/pid_$i"
        } &
    done
    
    sleep 5
    active_connections=$(pgrep -f "curl.*$DOMAIN" | wc -l)
    print_status "$active_connections connections started"
}

# Simulate backend failure/restart
simulate_migration() {
    print_info "Simulating backend migration events..."
    
    local migration_count=0
    local test_start=$(date +%s)
    
    while [ $(($(date +%s) - test_start)) -lt $((TEST_DURATION - 10)) ]; do
        sleep $MIGRATION_INTERVAL
        
        migration_count=$((migration_count + 1))
        print_warning "Migration event #$migration_count - simulating backend restart"
        
        # Test different migration scenarios
        case $((migration_count % 3)) in
            1)
                print_info "Scenario: Graceful backend restart"
                # In real deployment, this would restart a backend service
                curl $PROTOCOL -s "https://$DOMAIN/api/admin/restart?instance=2" >/dev/null 2>&1 || true
                ;;
            2)
                print_info "Scenario: Network path change simulation"
                # Simulate network route change
                curl $PROTOCOL -s "https://$DOMAIN/api/admin/network-change" >/dev/null 2>&1 || true
                ;;
            3)
                print_info "Scenario: Load balancer reconfiguration"
                # Simulate load balancer config change
                curl $PROTOCOL -s "https://$DOMAIN/api/admin/rebalance" >/dev/null 2>&1 || true
                ;;
        esac
        
        # Check connection health after migration
        sleep 2
        active=$(pgrep -f "curl.*$DOMAIN" | wc -l)
        print_info "Active connections after migration: $active"
    done
}

# Monitor connection results
monitor_results() {
    print_info "Monitoring connection results..."
    
    local success_count=0
    local failure_count=0
    local migration_count=0
    
    # Wait for all connections to complete
    wait
    
    # Analyze results
    for i in $(seq 1 $CONCURRENT_CONNECTIONS); do
        log_file="/tmp/quic-migration-test/connection_$i.log"
        
        if [ -f "$log_file" ]; then
            if grep -q "migration" "$log_file" 2>/dev/null; then
                migration_count=$((migration_count + 1))
            fi
            
            # Check if connection completed successfully
            if [ -s "$log_file" ] && ! grep -q "error\|failed\|timeout" "$log_file" 2>/dev/null; then
                success_count=$((success_count + 1))
            else
                failure_count=$((failure_count + 1))
            fi
        else
            failure_count=$((failure_count + 1))
        fi
    done
    
    # Calculate success rate
    success_rate=$((success_count * 100 / CONCURRENT_CONNECTIONS))
    
    echo
    print_info "Connection Migration Test Results:"
    echo "  Total Connections: $CONCURRENT_CONNECTIONS"
    echo "  Successful: $success_count"
    echo "  Failed: $failure_count"
    echo "  Success Rate: $success_rate%"
    echo "  Detected Migrations: $migration_count"
    
    if [ $success_rate -ge 90 ]; then
        print_status "Connection migration test PASSED (≥90% success rate)"
    elif [ $success_rate -ge 70 ]; then
        print_warning "Connection migration test MARGINAL (70-89% success rate)"
    else
        print_error "Connection migration test FAILED (<70% success rate)"
    fi
}

# Performance analysis
analyze_performance() {
    print_info "Analyzing performance metrics..."
    
    # Get current metrics
    metrics_response=$(curl $PROTOCOL -s "https://$DOMAIN/metrics" 2>/dev/null || echo "")
    
    if [ -n "$metrics_response" ]; then
        echo
        print_info "Key Performance Metrics:"
        
        # Extract QUIC-specific metrics
        echo "$metrics_response" | grep -E "(quic_connections|http3_requests|connection_migration)" | head -10
        
        # Connection migration metrics
        migrations=$(echo "$metrics_response" | grep "connection_migration_total" | awk '{print $2}' || echo "0")
        print_info "Total Connection Migrations: $migrations"
        
        # Average response time
        avg_response_time=$(echo "$metrics_response" | grep "http_request_duration_seconds" | grep "quantile=\"0.5\"" | awk '{print $2}' || echo "N/A")
        print_info "Median Response Time: ${avg_response_time}s"
        
    else
        print_warning "Could not retrieve performance metrics"
    fi
}

# Cleanup
cleanup() {
    print_info "Cleaning up test artifacts..."
    
    # Kill any remaining curl processes
    pkill -f "curl.*$DOMAIN" 2>/dev/null || true
    
    # Remove temp files
    rm -rf /tmp/quic-migration-test
    
    print_status "Cleanup completed"
}

# Main test execution
main() {
    print_banner
    
    # Setup
    check_dependencies
    test_connectivity
    test_load_balancing
    
    print_info "Starting $TEST_DURATION second connection migration test..."
    print_info "Domain: $DOMAIN"
    print_info "Protocol: $(echo $PROTOCOL | sed 's/--//')"
    print_info "Concurrent connections: $CONCURRENT_CONNECTIONS"
    print_info "Migration interval: ${MIGRATION_INTERVAL}s"
    echo
    
    # Execute test
    start_long_connections &
    CONNECTION_PID=$!
    
    simulate_migration &
    MIGRATION_PID=$!
    
    # Wait for test completion
    wait $CONNECTION_PID
    kill $MIGRATION_PID 2>/dev/null || true
    
    # Analyze results
    monitor_results
    analyze_performance
    
    # Cleanup
    cleanup
    
    echo
    print_status "Connection migration test completed!"
}

# Trap for cleanup on exit
trap cleanup EXIT

# Run main function
main "$@"
