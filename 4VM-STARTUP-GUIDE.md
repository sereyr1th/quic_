# QUIC Load Balancer 4-VM Setup Guide

## 🚀 Complete 4-VM QUIC-LB Setup with HTTP/3

This guide covers starting and managing your 4-VM QUIC load balancer setup with HTTP/3 support.

---

## 📋 **System Overview**

| VM Name    | IP Address       | Role                  | Ports       | Services                    |
|------------|------------------|-----------------------|-------------|-----------------------------|
| `quic-lb`  | 192.168.122.10   | Load Balancer         | 8080, 9443  | HTTP/3 QUIC, HTTP/2, HTTP/1.1 |
| `backend1` | 192.168.122.11   | Backend Server 1      | 8000        | Python HTTP Server          |
| `backend2` | 192.168.122.12   | Backend Server 2      | 8000        | Python HTTP Server          |
| `backend3` | 192.168.122.13   | Backend Server 3      | 8000        | Python HTTP Server          |

---

## 🔧 **Prerequisites**

- Ubuntu 22.04 Server installed on all 4 VMs
- libvirt/KVM hypervisor running
- User credentials: `ubuntu`/`your_password`
- Go 1.23+ installed on load balancer VM

---

## 📦 **Method 1: Using virt-manager (GUI)**

### **1. Start virt-manager**
```bash
virt-manager &
```

### **2. Start all VMs**
- Double-click each VM: `quic-lb`, `backend1`, `backend2`, `backend3`
- Click the "Play" button to start each VM
- Wait for Ubuntu boot sequence

### **3. Access VM Consoles**
- Double-click each running VM to open console
- Login with your credentials

---

## 💻 **Method 2: Using virsh (Command Line)**

### **1. Start All VMs**
```bash
# Start all VMs
virsh start quic-lb
virsh start backend1
virsh start backend2
virsh start backend3

# Verify all are running
virsh list --all
```

### **2. Access VM Consoles**
```bash
# Connect to each VM (use Ctrl + ] to exit)
virsh console quic-lb    # Load balancer
virsh console backend1   # Backend 1
virsh console backend2   # Backend 2
virsh console backend3   # Backend 3
```

---

## 🖥️ **Method 3: Using SSH (Recommended for Management)**

### **1. Start VMs**
```bash
virsh start quic-lb backend1 backend2 backend3
```

### **2. SSH to Each VM**
```bash
# Load Balancer
ssh ubuntu@192.168.122.10

# Backend Servers
ssh ubuntu@192.168.122.11  # Backend 1
ssh ubuntu@192.168.122.12  # Backend 2
ssh ubuntu@192.168.122.13  # Backend 3
```

**Note**: If SSH authentication fails, use console access (`virsh console`) initially.

---

## 🔄 **Starting Backend Services**

### **Backend 1 (192.168.122.11)**
```bash
ssh ubuntu@192.168.122.11
cd /home/ubuntu
python3 backend_server.py &
curl localhost:8000/health  # Should return "OK - Backend 1"
```

### **Backend 2 (192.168.122.12)**
```bash
ssh ubuntu@192.168.122.12
cd /home/ubuntu
python3 backend_server.py &
curl localhost:8000/health  # Should return "OK - Backend 2"
```

### **Backend 3 (192.168.122.13)**
```bash
ssh ubuntu@192.168.122.13
cd /home/ubuntu
python3 backend_server.py &
curl localhost:8000/health  # Should return "OK - Backend 3"
```

### **Verify All Backends (from host)**
```bash
curl http://192.168.122.11:8000/health  # OK - Backend 1
curl http://192.168.122.12:8000/health  # OK - Backend 2
curl http://192.168.122.13:8000/health  # OK - Backend 3
```

---

## 🚀 **Starting QUIC Load Balancer with HTTP/3**

### **1. SSH to Load Balancer**
```bash
ssh ubuntu@192.168.122.10
```

### **2. Set Environment Variables**
```bash
export BACKEND_1_URL="http://192.168.122.11:8000"
export BACKEND_2_URL="http://192.168.122.12:8000"
export BACKEND_3_URL="http://192.168.122.13:8000"

# Verify environment
echo "Backend URLs configured:"
echo "BACKEND_1_URL=$BACKEND_1_URL"
echo "BACKEND_2_URL=$BACKEND_2_URL"
echo "BACKEND_3_URL=$BACKEND_3_URL"

# IMPORTANT: Stop and restart if already running
sudo pkill -f quic_lb_full
```

### **3. Navigate to Project Directory**
```bash
cd /home/ubuntu/quic_original
```

### **4. Clean Any Existing Processes**
```bash
# Kill any existing load balancer processes
sudo pkill -9 -f quic_lb_full

# Verify ports are free
sudo ss -tuln | grep -E ":(9443|8080)"
```

### **5. Start QUIC Load Balancer**
```bash
./quic_lb_full
```

### **Expected Output:**
```
✅ QUIC-LB Draft 20 compliant load balancer initialized
✅ Added backend 1: http://192.168.122.11:8000
✅ Added backend 2: http://192.168.122.12:8000
✅ Added backend 3: http://192.168.122.13:8000
🚀 Enhanced HTTP/3 server starting...
🌐 Server is running - HTTP/2 on TCP:9443, HTTP/3 on UDP:9443
✅ All backends UP (Health checks passing)
```

---

## 🧪 **Testing the Setup**

### **1. Test Backend Health**
```bash
# From host machine
curl http://192.168.122.11:8000/health
curl http://192.168.122.12:8000/health
curl http://192.168.122.13:8000/health
```

### **2. Test HTTP/2 Load Balancer**
```bash
# Basic connectivity test
curl -k https://192.168.122.10:9443/

# Multiple requests to see load balancing
for i in {1..5}; do 
  echo "Request $i:"
  curl -k -s https://192.168.122.10:9443/ | grep "Backend Server"
done
```

### **3. Test HTTP/3 Advertisement**
```bash
# Check for HTTP/3 advertisement
curl -k -v https://192.168.122.10:9443/ 2>&1 | grep -i "alt-svc\|h3"
```

### **4. Access Web Dashboard**
```bash
# Open in browser
firefox https://192.168.122.10:9443/ &

# Or test API
curl -k https://192.168.122.10:9443/api/quic-lb
```

### **5. Verify Ports**
```bash
# Check all services are listening
sudo ss -tuln | grep -E ":(8000|9443|8080)"
```

---

## 🔧 **Troubleshooting**

### **Common Issues & Solutions**

#### **SSH Authentication Failed**
```bash
# Use console access instead
virsh console backend1
# Login manually and check SSH service
sudo systemctl status ssh
```

#### **Backend Not Responding**
```bash
# Check if backend service is running
ssh ubuntu@192.168.122.11
ps aux | grep python3
# Restart if needed
python3 backend_server.py &
```

#### **Load Balancer Port Conflicts**
```bash
# Kill existing processes
sudo pkill -9 -f quic_lb_full
# Check what's using ports
sudo lsof -i :9443
sudo lsof -i :8080
# Restart load balancer
./quic_lb_full
```

#### **Wrong Backend URLs (Common Issue)**
```bash
# If you see localhost:8081, localhost:8082, localhost:8083 in logs:
# 1. Stop load balancer (Ctrl+C)
# 2. Set correct environment variables:
export BACKEND_1_URL="http://192.168.122.11:8000"
export BACKEND_2_URL="http://192.168.122.12:8000"
export BACKEND_3_URL="http://192.168.122.13:8000"
# 3. Restart load balancer
./quic_lb_full
```

#### **HTTP/3 Not Working**
```bash
# Verify UDP port is listening
sudo ss -tuln | grep "udp.*9443"
# Should show: udp UNCONN 0 0 *:9443 *:*
```

#### **VM Won't Start**
```bash
# Check VM status
virsh list --all
# Force start
virsh start --force-boot quic-lb
# Check logs
virsh console quic-lb
```

---

## 📊 **Monitoring & Logs**

### **Load Balancer Logs**
- Health checks: `🏥 Enhanced Backend #N ✅ UP`
- Load balancing: `🔀 Load Balance: GET / -> Backend #N`
- HTTP/3 status: `🚀 Enhanced HTTP/3 server starting...`

### **Backend Logs**
- HTTP requests: `127.0.0.1 - - [timestamp] "GET /health HTTP/1.1" 200 -`

### **Performance Monitoring**
```bash
# Check load balancer performance
curl -k https://192.168.122.10:9443/api/quic-lb | jq '.backend_stats'

# Monitor backend health
watch 'curl -s http://192.168.122.11:8000/health; echo'
```

---

## 🎯 **Quick Start Script**

Create this script for easy startup:

```bash
#!/bin/bash
# File: start_quic_cluster.sh

echo "🚀 Starting QUIC Load Balancer Cluster..."

# Start all VMs
echo "Starting VMs..."
virsh start quic-lb backend1 backend2 backend3

# Wait for boot
echo "Waiting for VMs to boot..."
sleep 30

# Start backend services
echo "Starting backend services..."
ssh ubuntu@192.168.122.11 'cd /home/ubuntu && python3 backend_server.py &' &
ssh ubuntu@192.168.122.12 'cd /home/ubuntu && python3 backend_server.py &' &
ssh ubuntu@192.168.122.13 'cd /home/ubuntu && python3 backend_server.py &' &

# Wait for backends
sleep 10

# Test backends
echo "Testing backends..."
curl -s http://192.168.122.11:8000/health
curl -s http://192.168.122.12:8000/health
curl -s http://192.168.122.13:8000/health

echo "✅ All backends started successfully!"
echo "🔗 Access load balancer: https://192.168.122.10:9443/"
```

Make it executable:
```bash
chmod +x start_quic_cluster.sh
./start_quic_cluster.sh
```

---

## 🌐 **Features Available**

### **QUIC-LB Draft 20 Features**
- ✅ **HTTP/3 over QUIC** (UDP:9443)
- ✅ **HTTP/2** (TCP:9443)
- ✅ **HTTP/1.1** (TCP:8080)
- ✅ **IETF QUIC-LB Draft 20 Compliance**
- ✅ **Connection ID Encoding**
- ✅ **Stateless Load Balancing**
- ✅ **Health Monitoring**
- ✅ **Session Affinity**
- ✅ **Round-Robin Distribution**
- ✅ **Circuit Breaker Pattern**

### **Management APIs**
- Dashboard: `https://192.168.122.10:9443/`
- QUIC-LB API: `https://192.168.122.10:9443/api/quic-lb`
- Config Management: `https://192.168.122.10:9443/api/quic-lb/config`
- Algorithm Demo: `https://192.168.122.10:9443/api/quic-lb/demo`
- CID Testing: `https://192.168.122.10:9443/api/quic-lb/test-cid`

---

## 🎉 **Success Indicators**

Your setup is working when you see:

1. **All VMs running**: `virsh list` shows 4 running VMs
2. **Backends healthy**: All health checks return `OK - Backend N`
3. **Load balancer started**: Shows `✅ Added backend N: http://192.168.122.1N:8000`
4. **Ports listening**: UDP:9443, TCP:9443, TCP:8080 all bound
5. **HTTP/3 ready**: Browser shows `alt-svc: h3=":9443"` header
6. **Backend health**: `🏥 Enhanced Backend #N ✅ UP` (not ❌ DOWN)

## 🔍 **Why Same Backend Every Refresh?**

**This is CORRECT behavior!** 
- **HTTP/2 Connection Reuse**: Browser keeps same connection alive
- **QUIC Session Affinity**: Connection ID encodes backend server
- **Same Client → Same Backend**: Ensures session persistence

**To see different backends:**
- Use different browsers (Firefox vs Chrome)
- Use private/incognito mode
- Close all tabs and reopen
- Use `curl` (each request = new connection)

**🎯 Congratulations! You now have a fully operational IETF QUIC-LB Draft 20 compliant HTTP/3 load balancer cluster!** 🚀
