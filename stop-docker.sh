#!/bin/bash
set -e

echo "🛑 Stopping QUIC Load Balancer Stack..."

# Use docker compose (newer) or docker-compose (older)
if docker compose version > /dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
else
    COMPOSE_CMD="docker-compose"
fi

# Stop and remove containers, networks, and volumes
$COMPOSE_CMD down -v

# Remove any orphaned containers
$COMPOSE_CMD rm -f

echo "✅ All services stopped and cleaned up!"
echo ""
echo "💡 To start again: ./start-docker.sh"
