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
    echo "║      IETF QUIC-LB Draft 20 Compliant        ║"
    echo "║              HTTP/3 Load Balancer           ║"
    echo "║         Docker Infrastructure Manager        ║"
    echo "╚══════════════════════════════════════════════╝"
    echo -e "${NC}"
}

print_status() {
    echo -e "${GREEN}✅ $1${NC}"
}
~
print_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"~
}

# Function to check if Docker is running
check_docker() {
    if ! docker info >/dev/null 2>&1; then
        print_error "Docker is not running. Please start Docker first."
        exit 1
    fi
}

# Function to check if docker-compose is available
check_docker_compose() {
    if ! command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
        print_error "Docker Compose is not available. Please install Docker Compose."
        exit 1
    fi
}

# Function to get docker-compose command
get_compose_cmd() {
    if command -v docker-compose >/dev/null 2>&1; then
        echo "docker-compose"
    else
        echo "docker compose"
    fi
}

# Function to build the application
build() {
    print_info "Building QUIC Load Balancer Docker images..."
    
    local compose_cmd=$(get_compose_cmd)
    $compose_cmd -f $COMPOSE_FILE build --no-cache
    
    print_status "Build completed successfully!"
}

# Function to start services
start() {
    print_info "Starting QUIC infrastructure..."
    
    local compose_cmd=$(get_compose_cmd)
    $compose_cmd -f $COMPOSE_FILE up -d
    
    print_status "Services started successfully!"
    print_info "Services available at:"
    echo "  🌐 QUIC Load Balancer: https://localhost:9443"
    echo "  📊 Prometheus: http://localhost:9090"
    echo "  📈 Grafana: http://localhost:3000 (admin/admin)"
    echo "  📋 Node Exporter: http://localhost:9100"
}

# Function to stop services
stop() {
    print_info "Stopping QUIC infrastructure..."
    
    local compose_cmd=$(get_compose_cmd)
    $compose_cmd -f $COMPOSE_FILE down
    
    print_status "Services stopped successfully!"
}

# Function to restart services
restart() {
    print_info "Restarting QUIC infrastructure..."
    stop
    start
    print_status "Services restarted successfully!"
}

# Function to show logs
logs() {
    local service=${1:-}
    local compose_cmd=$(get_compose_cmd)
    
    if [ -z "$service" ]; then
        $compose_cmd -f $COMPOSE_FILE logs -f
    else
        $compose_cmd -f $COMPOSE_FILE logs -f "$service"
    fi
}

# Function to show status
status() {
    print_info "Checking service status..."
    
    local compose_cmd=$(get_compose_cmd)
    $compose_cmd -f $COMPOSE_FILE ps
}

# Function to run performance tests
test_performance() {
    print_info "Running performance tests..."
    
    # Check if the test script exists
    if [ ! -f "./test_quic_performance.sh" ]; then
        print_error "Performance test script not found!"
        exit 1
    fi
    
    # Make sure the script is executable
    chmod +x ./test_quic_performance.sh
    
    # Run the test
    ./test_quic_performance.sh
}

# Function to clean up everything
cleanup() {
    print_warning "This will remove all containers, volumes, and images. Are you sure? (y/N)"
    read -r response
    
    if [[ "$response" =~ ^[Yy]$ ]]; then
        print_info "Cleaning up QUIC infrastructure..."
        
        local compose_cmd=$(get_compose_cmd)
        $compose_cmd -f $COMPOSE_FILE down -v --rmi all
        
        # Remove any dangling containers and images
        docker container prune -f
        docker image prune -f
        docker volume prune -f
        
        print_status "Cleanup completed!"
    else
        print_info "Cleanup cancelled."
    fi
}

# Function to show health status
health() {
    print_info "Checking service health..."
    
    echo "🔍 QUIC Server Health:"
    curl -k -s https://localhost:9443/health || echo "❌ QUIC server not responding"
    
    echo "🔍 Backend Services Health:"
    for port in 8081 8082 8083; do
        response=$(curl -s http://localhost:$port/ || echo "failed")
        if [[ "$response" == "failed" ]]; then
            echo "❌ Backend on port $port not responding"
        else
            echo "✅ Backend on port $port is healthy"
        fi
    done
    
    echo "🔍 Monitoring Health:"
    prometheus_status=$(curl -s http://localhost:9090/-/healthy || echo "failed")
    if [[ "$prometheus_status" == "Prometheus is Healthy." ]]; then
        echo "✅ Prometheus is healthy"
    else
        echo "❌ Prometheus not responding"
    fi
    
    grafana_status=$(curl -s http://localhost:3000/api/health || echo "failed")
    if [[ "$grafana_status" =~ "ok" ]]; then
        echo "✅ Grafana is healthy"
    else
        echo "❌ Grafana not responding"
    fi
}

# Function to update services
update() {
    print_info "Updating QUIC infrastructure..."
    
    local compose_cmd=$(get_compose_cmd)
    
    # Pull latest images
    $compose_cmd -f $COMPOSE_FILE pull
    
    # Rebuild our custom image
    $compose_cmd -f $COMPOSE_FILE build --no-cache quic-server
    
    # Restart with new images
    $compose_cmd -f $COMPOSE_FILE up -d
    
    print_status "Update completed!"
}

# Function to show help
show_help() {
    echo "QUIC Load Balancer Management Script"
    echo ""
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  build       Build Docker images"
    echo "  start       Start all services"
    echo "  stop        Stop all services"
    echo "  restart     Restart all services"
    echo "  status      Show service status"
    echo "  logs [svc]  Show logs (optionally for specific service)"
    echo "  test        Run performance tests"
    echo "  health      Check service health"
    echo "  update      Update and restart services"
    echo "  cleanup     Remove all containers, volumes, and images"
    echo "  help        Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0 start                 # Start all services"
    echo "  $0 logs quic-server      # Show logs for QUIC server only"
    echo "  $0 test                  # Run performance tests"
}

# Main script logic
main() {
    print_banner
    
    # Check prerequisites
    check_docker
    check_docker_compose
    
    case "${1:-help}" in
        build)
            build
            ;;
        start)
            start
            ;;
        stop)
            stop
            ;;
        restart)
            restart
            ;;
        logs)
            logs "$2"
            ;;
        status)
            status
            ;;
        test)
            test_performance
            ;;
        health)
            health
            ;;
        update)
            update
            ;;
        cleanup)
            cleanup
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            print_error "Unknown command: $1"
            show_help
            exit 1
            ;;
    esac
}

# Run main function with all arguments
main "$@"
