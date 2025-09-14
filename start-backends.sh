#!/bin/bash
echo "🏪 Starting backend servers..."

# Start backend server 1
echo "Starting backend #1 on port 8081..."
(cd static && python3 -m http.server 8081) &
BACKEND1_PID=$!

# Start backend server 2  
echo "Starting backend #2 on port 8082..."
(cd static && python3 -m http.server 8082) &
BACKEND2_PID=$!

# Start backend server 3
echo "Starting backend #3 on port 8083..."
(cd static && python3 -m http.server 8083) &
BACKEND3_PID=$!

echo "✅ All backend servers started!"
echo "Backend #1 PID: $BACKEND1_PID (serving static/ folder)"
echo "Backend #2 PID: $BACKEND2_PID (serving static/ folder)" 
echo "Backend #3 PID: $BACKEND3_PID (serving static/ folder)"

echo ""
echo "🌐 Backend URLs:"
echo "   Backend #1: http://localhost:8081 (static files)"
echo "   Backend #2: http://localhost:8082 (static files)"
echo "   Backend #3: http://localhost:8083 (static files)"
echo ""
echo "To stop backends: kill $BACKEND1_PID $BACKEND2_PID $BACKEND3_PID"
echo "Or run: pkill -f 'python3 -m http.server'"
echo ""
echo "Now run your main server: go run main.go"

# Wait for user input to stop
read -p "Press Enter to stop all backends..."
kill $BACKEND1_PID $BACKEND2_PID $BACKEND3_PID 2>/dev/null
echo "🛑 All backends stopped."
