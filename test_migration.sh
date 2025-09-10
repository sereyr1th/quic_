#!/bin/bash

echo "🧪 QUIC Connection Migration Test Script"
echo "========================================"
echo ""

# Check if server is running
if ! curl -s -k https://localhost:9443/api/test > /dev/null 2>&1; then
    echo "❌ Server not running! Please start with: ./quic-server"
    exit 1
fi

echo "✅ Server is running!"
echo ""

echo "🔗 Testing Connection Tracking..."
echo "1. Making initial requests to establish connections:"

# Make several requests to establish connections
for i in {1..3}; do
    echo "   Request $i:"
    response=$(curl -s -k https://localhost:9443/api/test)
    protocol=$(echo $response | grep -o '"protocol":"[^"]*"' | cut -d'"' -f4)
    echo "     Protocol: $protocol"
    sleep 1
done

echo ""
echo "2. Checking connection status:"
curl -s -k https://localhost:9443/api/connections | jq '.'

echo ""
echo "🧪 Manual Migration Testing Instructions:"
echo "========================================="
echo ""
echo "To test REAL connection migration, you need to:"
echo ""
echo "1. 📱 Open https://localhost:9443 in a browser on your phone/laptop"
echo "2. 🔄 Click 'Test API Endpoint' several times while on WiFi"
echo "3. 🌐 Switch your device to mobile hotspot/different WiFi"
echo "4. 🔄 Click 'Test API Endpoint' again"
echo "5. 📊 Click 'View Connections' to see migration events"
echo ""
echo "Expected Results:"
echo "- Same connection ID should be used across network changes"
echo "- Migration events should be logged in server console"
echo "- You should see 🔄 emoji in server logs for migrations"
echo ""
echo "🌐 Browser Testing URLs:"
echo "- Main page: https://localhost:9443"
echo "- Connection monitor: https://localhost:9443/api/connections"
echo ""
echo "🔧 Tips for Testing:"
echo "- Use Chrome/Edge for best HTTP/3 support"
echo "- Enable chrome://flags/#enable-quic"
echo "- Use developer tools to verify HTTP/3 usage"
echo "- Try on mobile device for easier network switching"
echo ""
