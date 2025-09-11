#!/bin/bash

echo "🚀 QUIC Connection Migration Testing - Local Network"
echo "=================================================="
echo ""

# Function to get local IP
get_local_ip() {
    ip route get 8.8.8.8 | awk '{print $7; exit}'
}

# Build the server
echo "📦 Building QUIC server..."
go build -o quic-server main.go

if [ $? -ne 0 ]; then
    echo "❌ Failed to build server"
    exit 1
fi

echo "✅ Server built successfully"
echo ""

# Get current IP
LOCAL_IP=$(get_local_ip)

# Start the QUIC server in background
echo "🔧 Starting QUIC server on localhost:9443..."
./quic-server &
SERVER_PID=$!

# Wait a moment for server to start
sleep 3

# Check if server is running
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "❌ Server failed to start"
    exit 1
fi

echo "✅ QUIC server is running (PID: $SERVER_PID)"
echo ""

echo "� Local Network Testing URLs:"
echo "============================="
echo ""
echo "� Phone (WiFi): https://$LOCAL_IP:9443"
echo "🖥️  Desktop: https://localhost:9443"
echo "🧪 API Test: https://$LOCAL_IP:9443/api/test"
echo "📊 Connections: https://$LOCAL_IP:9443/api/connections"
echo ""

echo "🎯 HTTP/3 MIGRATION TESTING:"
echo "============================"
echo ""
echo "1. 📱 On your phone, connect to same WiFi network"
echo "2. 🌐 Open: https://$LOCAL_IP:9443"
echo "3. ⚠️  Accept security warning (self-signed cert)"
echo "4. 🧪 Test /api/test endpoint"
echo "5. 🔄 For migration testing, try:"
echo "   • Switch between 2.4GHz and 5GHz WiFi bands"
echo "   • Move between WiFi access points (mesh network)"
echo "   • Use mobile hotspot setup"
echo ""

echo "🔍 Watch for these indicators:"
echo "============================="
echo "• 🚀 emoji = HTTP/3 request detected"
echo "• 🔄 emoji = Connection migration detected"
echo "• Protocol: HTTP/3.0 🚀 in API responses"
echo ""

echo "💡 Why local testing is better:"
echo "==============================="
echo "✅ Real HTTP/3/QUIC protocol (not proxied)"
echo "✅ Direct UDP connection"
echo "✅ Authentic migration behavior"
echo "✅ No tunnel service limitations"
echo ""

echo "⏹️  Press Ctrl+C to stop"

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

# Wait for user to stop
wait $SERVER_PID
