#!/bin/bash

# Turnip WebRTC Signaling Server Metrics Test Script

echo "🚀 Starting Turnip WebRTC Signaling Server with Metrics..."

# Start the server in the background
ACCESS_SECRET=test-secret ./tmp/main &
SERVER_PID=$!

# Wait for server to start
sleep 3

echo "📊 Testing Metrics Endpoints..."

echo ""
echo "=== Health Check ==="
curl -s http://localhost:9090/health
echo ""

echo ""
echo "=== Server Info ==="
curl -s http://localhost:9090/info | jq .
echo ""

echo ""
echo "=== Initial Metrics (Turnip specific) ==="
curl -s http://localhost:9090/metrics | grep turnip | head -10
echo ""

echo ""
echo "🔐 Testing Authentication..."

# Generate a test token
echo "Generating test token..."
TOKEN_RESPONSE=$(curl -s -X POST http://localhost:5004/generate-token \
  -H "Content-Type: application/json" \
  -d '{"username": "testuser"}')

TOKEN=$(echo $TOKEN_RESPONSE | jq -r .token)
echo "Generated token: ${TOKEN:0:50}..."
echo ""

# Test valid authentication (this will trigger auth metrics)
echo "Testing valid authentication..."
curl -s "http://localhost:5004/ws?token=$TOKEN" -I | head -5
echo ""

# Test invalid authentication (this will trigger failure metrics)
echo "Testing invalid authentication..."
curl -s "http://localhost:5004/ws?token=invalid" -I | head -5
echo ""

echo ""
echo "📈 Updated Metrics After Authentication Tests..."
curl -s http://localhost:9090/metrics | grep -E "(turnip_auth|turnip_token)" 
echo ""

echo ""
echo "💾 Memory and Runtime Metrics..."
curl -s http://localhost:9090/metrics | grep -E "(turnip_memory|turnip_heap|turnip_goroutines|turnip_uptime)"
echo ""

echo "🏁 Test complete! Server PID: $SERVER_PID"
echo "To stop the server: kill $SERVER_PID"
echo "To view real-time metrics: watch 'curl -s http://localhost:9090/metrics | grep turnip'"
echo "To view logs: tail -f the server output"
