#!/bin/bash
set -e

echo "🚀 Starting QUIC Load Balancer with Monitoring Stack..."

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo "❌ Docker is not running. Please start Docker first."
    exit 1
fi

# Check if docker-compose is available
if ! command -v docker-compose > /dev/null 2>&1 && ! docker compose version > /dev/null 2>&1; then
    echo "❌ Docker Compose is not available. Please install Docker Compose."
    exit 1
fi

# Create necessary directories
mkdir -p monitoring/grafana/dashboards
mkdir -p monitoring/grafana/provisioning/dashboards
mkdir -p monitoring/grafana/provisioning/datasources

echo "📦 Building and starting all services..."

# Use docker compose (newer) or docker-compose (older)
if docker compose version > /dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
else
    COMPOSE_CMD="docker-compose"
fi

# Stop any existing containers
echo "🛑 Stopping existing containers..."
$COMPOSE_CMD down -v

# Build and start all services
echo "🏗️ Building and starting services..."
$COMPOSE_CMD up --build -d

# Wait for services to be ready
echo "⏳ Waiting for services to start..."
sleep 10

# Check service status
echo "📊 Service Status:"
$COMPOSE_CMD ps

echo ""
echo "✅ QUIC Load Balancer Stack Started Successfully!"
echo ""
echo "🌐 Access URLs:"
echo "   • QUIC Load Balancer: https://localhost:9443"
echo "   • HTTP Load Balancer:  http://localhost:8080"
echo "   • Grafana Dashboard:   http://localhost:3000 (admin/admin123)"
echo "   • Prometheus:          http://localhost:9090"
echo "   • Backend 1:           http://localhost:8081"
echo "   • Backend 2:           http://localhost:8082"
echo "   • Backend 3:           http://localhost:8083"
echo ""
echo "📊 To view logs: $COMPOSE_CMD logs -f"
echo "🛑 To stop:      $COMPOSE_CMD down"
echo ""
echo "🎯 Ready for testing! Try accessing https://localhost:9443"
