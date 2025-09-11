#!/bin/bash

echo "🚀 QUIC HTTP/3 Migration Testing - Local Network Approach"
echo "========================================================"
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

# Start the QUIC server
echo "🔧 Starting QUIC server..."
./quic-server &
SERVER_PID=$!

sleep 3

if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "❌ Server failed to start"
    exit 1
fi

echo "✅ QUIC server is running (PID: $SERVER_PID)"
echo ""

echo "🌐 HTTP/3 Testing URLs:"
echo "======================"
echo ""
echo "📱 Phone (WiFi): https://$LOCAL_IP:9443"
echo "🖥️  Desktop: https://localhost:9443"
echo "🧪 API Test: https://$LOCAL_IP:9443/api/test"
echo "📊 Connections: https://$LOCAL_IP:9443/api/connections"
echo ""

echo "🔥 HTTP/3 Testing Instructions:"
echo "==============================="
echo ""
echo "1. ✅ Make sure your phone and computer are on the SAME WiFi"
echo "2. 📱 Open Chrome on your phone with HTTP/3 enabled:"
echo "   chrome://flags/#enable-quic"
echo "3. 🌐 Visit: https://$LOCAL_IP:9443"
echo "4. ⚠️  Accept the security warning (self-signed cert)"
echo "5. 🧪 Test /api/test endpoint multiple times"
echo "6. 🔍 Check Chrome DevTools -> Network tab -> Protocol column"
echo "7. 📊 Visit /api/connections to see connection info"
echo ""

echo "🎯 For REAL HTTP/3 Migration Testing:"
echo "====================================="
echo ""
echo "Option A: WiFi Band Switching (Same network)"
echo "• Switch between 2.4GHz and 5GHz WiFi bands"
echo "• Keep same browser tab open"
echo "• Connection should migrate seamlessly"
echo ""
echo "Option B: Mobile Hotspot Testing"
echo "• Connect laptop to your phone's hotspot"
echo "• Phone acts as server host"
echo "• Switch phone between WiFi networks"
echo "• Test from laptop browser"
echo ""
echo "Option C: Router with Multiple Access Points"
echo "• Use mesh network or WiFi extenders"
echo "• Move between different access points"
echo "• Same network, different connection paths"
echo ""

echo "🚨 Why ngrok doesn't work for HTTP/3:"
echo "======================================"
echo ""
echo "❌ ngrok only supports HTTP/1.1 and HTTP/2"
echo "❌ ngrok cannot forward UDP traffic (required for QUIC)"
echo "❌ QUIC/HTTP/3 requires direct UDP connection"
echo "✅ Local network testing is the most accurate"
echo ""

echo "🔍 Look for these indicators:"
echo "=========================="
echo "• 🚀 emoji in server logs = HTTP/3 request"
echo "• 🔄 emoji in server logs = Connection migration"
echo "• Protocol: HTTP/3.0 🚀 in API responses"
echo ""

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "🛑 Stopping server..."
    kill $SERVER_PID 2>/dev/null
    echo "✅ Cleanup complete"
    exit 0
}

trap cleanup SIGINT SIGTERM

echo "⏹️  Press Ctrl+C to stop"
echo ""

# Keep running
wait $SERVER_PID
