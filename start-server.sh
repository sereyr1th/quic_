#!/bin/bash

echo "🚀 QUIC Connection Migration Testing"
echo "===================================="
echo ""

# Function to check if port is in use
check_port() {
    local port=$1
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1 || lsof -Pi :$port -sUDP:Unconn -t >/dev/null 2>&1; then
        return 0  # Port is in use
    else
        return 1  # Port is free
    fi
}

# Function to kill processes on port
kill_port() {
    local port=$1
    echo "🔧 Killing processes on port $port..."
    lsof -ti :$port | xargs -r kill -9 >/dev/null 2>&1
    sleep 1
}

# Check and free ports
if check_port 9443; then
    echo "⚠️  Port 9443 is in use, attempting to free it..."
    kill_port 9443
fi

if check_port 8080; then
    echo "⚠️  Port 8080 is in use, attempting to free it..."
    kill_port 8080
fi

# Build the server
echo "📦 Building QUIC server..."
go build -o quic-server main.go

if [ $? -ne 0 ]; then
    echo "❌ Failed to build server"
    exit 1
fi

echo "✅ Server built successfully"
echo ""

# Start the QUIC server in background
echo "🔧 Starting QUIC server on localhost:9443..."
./quic-server &
SERVER_PID=$!

# Wait a moment for server to start
sleep 5

# Check if server is running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "❌ Server failed to start"
    echo "🔍 Checking server output..."
    wait $SERVER_PID
    exit 1
fi

echo "✅ QUIC server is running (PID: $SERVER_PID)"
echo ""

# Get current IP
LOCAL_IP=$(ip route get 8.8.8.8 | awk '{print $7; exit}')

echo "� LOCAL NETWORK TESTING:"
echo "========================"
echo ""
echo "📱 Phone (WiFi): https://$LOCAL_IP:9443"
echo "🖥️  Desktop: https://localhost:9443"
echo "🧪 API Test: https://$LOCAL_IP:9443/api/test"
echo "📊 Connections: https://$LOCAL_IP:9443/api/connections"
echo ""
echo "Option 1: Local Network Testing (RECOMMENDED):"
echo "  • Use the IP above for real HTTP/3 testing"
echo "  • Direct QUIC/UDP connection - no proxy limitations"
echo "  • Best for authentic migration behavior"
echo ""
echo "Option 2: Migration Testing Scenarios:"
echo "  • Switch between 2.4GHz and 5GHz WiFi bands"
echo "  • Move between WiFi access points (mesh network)"
echo "  • Use mobile hotspot for controlled testing"
echo ""
echo "Option 3: Alternative Tunnels (Limited HTTP/3 support):"
echo "  • localtunnel: npm install -g localtunnel; lt --port 9443"
echo "  • SSH tunnel: ssh -R 9443:localhost:9443 user@server"
echo "  • cloudflared: cloudflared tunnel --url https://localhost:9443"
echo ""

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "🛑 Stopping server..."
    kill $SERVER_PID 2>/dev/null
    echo "✅ Cleanup complete"
    exit 0
}

# Set trap to cleanup on exit
trap cleanup SIGINT SIGTERM

echo "🎯 CURRENT TESTING OPTIONS:"
echo "=========================="
echo ""
echo "While server is running, you can:"
echo "1. Test locally: https://localhost:9443"
echo "2. Test on local network: https://192.168.0.180:9443"
echo "3. Set up one of the tunnel options above"
echo ""
echo "🔍 Watch for '🔄 Connection Migration detected!' in the logs"
echo ""
echo "⏹️  Press Ctrl+C to stop"

# Wait for user to stop
wait $SERVER_PID
