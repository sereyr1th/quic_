#!/bin/bash

echo "📱 QUIC Connection Migration - Mobile Testing Setup"
echo "================================================="
echo ""

# Get local IP address
LOCAL_IP=$(hostname -I | awk '{print $1}')
echo "🌐 Your computer's IP address: $LOCAL_IP"
echo ""

# Check if server is running
echo "🔍 Checking server status..."
if curl -s -k https://localhost:9443/api/test > /dev/null 2>&1; then
    echo "✅ HTTPS server is running on port 9443"
else
    echo "❌ HTTPS server is not running"
    echo "   Please start with: ./quic-server"
    exit 1
fi

if curl -s http://localhost:8080/api/test > /dev/null 2>&1; then
    echo "✅ HTTP fallback server is running on port 8080"
else
    echo "❌ HTTP fallback server is not running"
fi

echo ""
echo "📱 Mobile Testing Instructions:"
echo "=============================="
echo ""
echo "1. 🔗 Connect your phone to the same WiFi network as this computer"
echo ""
echo "2. 🌐 Open your mobile browser and go to:"
echo "   📱 HTTPS (preferred): https://$LOCAL_IP:9443"
echo "   🌐 HTTP (fallback):   http://$LOCAL_IP:8080"
echo ""
echo "3. 🔒 Accept any security warnings about the certificate"
echo ""
echo "4. 🧪 Test connection migration:"
echo "   • Click 'Test API Endpoint' while on WiFi"
echo "   • Note your connection ID and IP address"
echo "   • Switch your phone to mobile data/cellular"
echo "   • Click 'Test API Endpoint' again"
echo "   • The same connection should continue working!"
echo "   • Check 'View Connections' to see migration events"
echo ""
echo "5. 🔄 Expected results:"
echo "   • Same Connection ID across network changes"
echo "   • Server logs show migration events with 🔄 emoji"
echo "   • Your IP address changes but connection persists"
echo ""
echo "🔧 Troubleshooting Tips:"
echo "======================="
echo "• If HTTPS doesn't work, try HTTP first"
echo "• Make sure both devices are on the same WiFi initially"
echo "• Some mobile browsers work better than others for HTTP/3"
echo "• Check your computer's firewall if connection fails"
echo ""
echo "📊 Monitor server logs in this terminal to see migration events!"
echo "Look for:"
echo "  🆕 New QUIC connection: (when you first connect)"
echo "  🔄 Connection Migration detected: (when you switch networks)"
echo ""

# Test connectivity from the server side
echo "🧪 Testing network connectivity..."
echo "Computer IP: $LOCAL_IP"
echo "HTTPS URL: https://$LOCAL_IP:9443"
echo "HTTP URL: http://$LOCAL_IP:8080"
echo ""
echo "🚀 Ready for mobile testing!"
