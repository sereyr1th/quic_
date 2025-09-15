#!/bin/bash

# QUIC Server Docker Management Script
# This script manages the complete QUIC infrastructure

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROJECT_NAME="quic-infrastructure"
COMPOSE_FILE="docker-compose.yml"

print_banner() {
    echo -e "${BLUE}"
    echo "╔══════════════════════════════════════════════╗"
    echo "║              QUIC HTTP/3 Server              ║"
    echo "║         Docker Infrastructure Manager        ║"
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

# Function to check if Docker is running
check_docker() {
    if ! docker info >/dev/null 2>&1; then
        print_error "Docker is not running. Please start Docker first."
        exit 1
    fi
    print_status "Docker is running"
}

# Function to check if docker-compose is available
check_docker_compose() {
    if ! command -v docker-compose >/dev/null 2>&1; then
        if ! docker compose version >/dev/null 2>&1; then
            print_error "Docker Compose is not available. Please install Docker Compose."
            exit 1
        else
            DOCKER_COMPOSE_CMD="docker compose"
        fi
    else
        DOCKER_COMPOSE_CMD="docker-compose"
    fi
    print_status "Docker Compose is available"
}

# Function to build and start all services
start_services() {
    print_info "Building and starting QUIC infrastructure..."
    
    # Build the main application
    print_info "Building QUIC server image..."
    $DOCKER_COMPOSE_CMD build quic-server
    
    # Start all services
    print_info "Starting all services..."
    $DOCKER_COMPOSE_CMD up -d
    
    print_status "All services started successfully!"
    
    # Wait a moment for services to initialize
    sleep 5
    
    # Show service status
    show_status
}

# Function to stop all services
stop_services() {
    print_info "Stopping all services..."
    $DOCKER_COMPOSE_CMD down
    print_status "All services stopped"
}

# Function to show service status
show_status() {
    echo
    print_info "Service Status:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # Check each service
    services=("quic-server" "quic-backend1" "quic-backend2" "quic-backend3" "quic-prometheus" "quic-grafana")
    
    for service in "${services[@]}"; do
        if docker ps --format "table {{.Names}}" | grep -q "^${service}$"; then
            print_status "$service is running"
        else
            print_error "$service is not running"
        fi
    done
    
    echo
    print_info "Service URLs:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "🚀 QUIC Server (HTTP/3):     https://localhost:9443"
    echo "🌐 HTTP Server (fallback):   http://localhost:8080"
    echo "📊 Prometheus:               http://localhost:9090"
    echo "📈 Grafana:                  http://localhost:3000 (admin/admin123)"
    echo "🔧 Backend 1:                http://localhost:8081"
    echo "🔧 Backend 2:                http://localhost:8082"
    echo "🔧 Backend 3:                http://localhost:8083"
    echo
    print_info "API Endpoints:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "🔗 Connections:              https://localhost:9443/api/connections"
    echo "⚖️  Load Balancer:            https://localhost:9443/api/loadbalancer"
    echo "🧪 Test Endpoint:             https://localhost:9443/api/test"
    echo "🔄 Migration Simulation:      https://localhost:9443/api/simulate-migration"
    echo "📊 Prometheus Metrics:        https://localhost:9443/metrics"
    echo
}

# Function to show logs
show_logs() {
    local service=${1:-""}
    
    if [ -z "$service" ]; then
        print_info "Showing logs for all services..."
        $DOCKER_COMPOSE_CMD logs -f
    else
        print_info "Showing logs for $service..."
        $DOCKER_COMPOSE_CMD logs -f "$service"
    fi
}

# Function to restart services
restart_services() {
    print_info "Restarting services..."
    $DOCKER_COMPOSE_CMD restart
    print_status "Services restarted"
    show_status
}

# Function to clean up everything
cleanup() {
    print_warning "This will remove all containers, networks, and volumes!"
    read -p "Are you sure? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        print_info "Cleaning up everything..."
        $DOCKER_COMPOSE_CMD down -v --remove-orphans
        docker system prune -f
        print_status "Cleanup completed"
    else
        print_info "Cleanup cancelled"
    fi
}

# Function to run performance tests
run_tests() {
    print_info "Running QUIC performance tests..."
    
    # Check if server is running
    if ! docker ps --format "table {{.Names}}" | grep -q "^quic-server$"; then
        print_error "QUIC server is not running. Please start services first."
        exit 1
    fi
    
    # Run the performance test script
    if [ -f "./test_quic_performance.sh" ]; then
        chmod +x ./test_quic_performance.sh
        ./test_quic_performance.sh
    else
        print_error "Performance test script not found"
    fi
}

# Function to open dashboards
open_dashboards() {
    print_info "Opening monitoring dashboards..."
    
    # Check if services are running
    if ! docker ps --format "table {{.Names}}" | grep -q "^quic-grafana$"; then
        print_error "Grafana is not running. Please start services first."
        exit 1
    fi
    
    # Open dashboards in browser (if available)
    if command -v xdg-open >/dev/null 2>&1; then
        xdg-open "http://localhost:3000" >/dev/null 2>&1 &
        xdg-open "http://localhost:9090" >/dev/null 2>&1 &
        print_status "Dashboards opened in browser"
    elif command -v open >/dev/null 2>&1; then
        open "http://localhost:3000" >/dev/null 2>&1 &
        open "http://localhost:9090" >/dev/null 2>&1 &
        print_status "Dashboards opened in browser"
    else
        print_info "Please open the following URLs manually:"
        echo "  📈 Grafana: http://localhost:3000"
        echo "  📊 Prometheus: http://localhost:9090"
    fi
}

# Main menu
show_menu() {
    echo
    print_info "Available commands:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "🚀 start      - Build and start all services"
    echo "🛑 stop       - Stop all services"
    echo "🔄 restart    - Restart all services"
    echo "📊 status     - Show service status and URLs"
    echo "📝 logs       - Show logs (add service name for specific logs)"
    echo "🧪 test       - Run performance tests"
    echo "📈 dashboard  - Open monitoring dashboards"
    echo "🧹 cleanup    - Remove all containers and volumes"
    echo "❓ help       - Show this menu"
    echo
}

# Main script logic
main() {
    print_banner
    
    # Check prerequisites
    check_docker
    check_docker_compose
    
    # Handle command line arguments
    case "${1:-help}" in
        "start")
            start_services
            ;;
        "stop")
            stop_services
            ;;
        "restart")
            restart_services
            ;;
        "status")
            show_status
            ;;
        "logs")
            show_logs "$2"
            ;;
        "test")
            run_tests
            ;;
        "dashboard")
            open_dashboards
            ;;
        "cleanup")
            cleanup
            ;;
        "help"|*)
            show_menu
            ;;
    esac
}

# Run main function with all arguments
main "$@"
