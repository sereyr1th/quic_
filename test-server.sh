#!/bin/bash

echo "🧪 Quick Test Script for QUIC Server"
echo "===================================="

# Build and start server briefly to test
echo "📦 Building server..."
go build -o quic-server main.go

echo "🔧 Starting server for quick test..."
timeout 10s ./quic-server &
SERVER_PID=$!

sleep 3

# Test the endpoints
echo "🌐 Testing HTTP endpoint..."
curl -s http://localhost:8080/api/test || echo "❌ HTTP test failed"

echo ""
echo "🔒 Testing HTTPS endpoint..."
curl -s -k https://localhost:9443/api/test || echo "❌ HTTPS test failed"

echo ""
echo "📊 Testing connections endpoint..."
curl -s -k https://localhost:9443/api/connections || echo "❌ Connections test failed"

# Cleanup
kill $SERVER_PID 2>/dev/null

echo ""
echo "✅ Basic tests complete!"
echo ""
echo "🚀 Ready for ngrok testing! Run: ./start-with-ngrok.sh"
