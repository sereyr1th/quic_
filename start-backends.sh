#!/bin/bash
set -e

echo "🏪 Starting backend servers..."

# Check if static directory exists
if [ ! -d "static" ]; then
    echo "❌ Error: static directory not found!"
    echo "Please make sure you're running this script from the project root directory."
    exit 1
fi

# Function to check if a port is already in use
check_port() {
    local port=$1
    if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
        echo "⚠️  Port $port is already in use. Skipping..."
        return 1
    fi
    return 0
}

# Function to start backend server
start_backend() {
    local port=$1
    local backend_num=$2
    
    if check_port $port; then
        echo "Starting backend #$backend_num on port $port..."
        (cd static && python3 -m http.server $port) &
        local pid=$!
        echo "✅ Backend #$backend_num started with PID: $pid"
        return $pid
    else
        return 0
    fi
}

# Start backend servers
BACKEND_PIDS=()

if start_backend 8081 1; then
    BACKEND_PIDS+=($(echo $!))
fi

if start_backend 8082 2; then
    BACKEND_PIDS+=($(echo $!))
fi

if start_backend 8083 3; then
    BACKEND_PIDS+=($(echo $!))
fi

# Give servers time to start
sleep 2

echo ""
echo "✅ Backend servers setup complete!"
echo "Active backends: ${#BACKEND_PIDS[@]}"

if [ ${#BACKEND_PIDS[@]} -gt 0 ]; then
    echo ""
    echo "🌐 Backend URLs:"
    echo "   Backend #1: http://localhost:8081 (static files)"
    echo "   Backend #2: http://localhost:8082 (static files)" 
    echo "   Backend #3: http://localhost:8083 (static files)"
    echo ""
    echo "📋 Backend PIDs: ${BACKEND_PIDS[*]}"
    echo ""
    echo "🔧 To stop all backends, run:"
    echo "   kill ${BACKEND_PIDS[*]}"
    echo ""
    echo "🚀 Now you can start the main server with:"
    echo "   go run main.go"
else
    echo "❌ No backend servers were started (all ports may be in use)"
    exit 1
fi
echo "Or run: pkill -f 'python3 -m http.server'"
echo ""
echo "Now run your main server: go run main.go"

# Wait for user input to stop
read -p "Press Enter to stop all backends..."
kill $BACKEND1_PID $BACKEND2_PID $BACKEND3_PID 2>/dev/null
echo "🛑 All backends stopped."
