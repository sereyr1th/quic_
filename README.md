# QUIC Load Balancer with Docker

A high-performance HTTP/3 QUIC load balancer with comprehensive monitoring stack, fully containerized with Docker.

## 🚀 Quick Start

### Prerequisites
- Docker and Docker Compose
- Ports 8080, 8081-8083, 9090, 9100, 9443, 3000 available

### Start All Services
```bash
docker compose up -d
```

### Stop All Services
```bash
docker compose down
```

### View Logs
```bash
docker compose logs -f
```

## 📋 Services

| Service | URL | Description |
|---------|-----|-------------|
| **QUIC Load Balancer** | https://localhost:9443<br>http://localhost:8080 | Main HTTP/3 QUIC load balancer |
| **Backend 1** | http://localhost:8081 | Python HTTP server |
| **Backend 2** | http://localhost:8082 | Python HTTP server |
| **Backend 3** | http://localhost:8083 | Python HTTP server |
| **Prometheus** | http://localhost:9090 | Metrics collection |
| **Grafana** | http://localhost:3000 | Dashboards (admin/admin123) |
| **Node Exporter** | http://localhost:9100 | System metrics |

## 🎯 Features

- **HTTP/3 QUIC Support**: Modern protocol with multiplexing and reduced latency
- **Load Balancing Algorithms**: Weighted round-robin, health-based, consistent hashing
- **Health Monitoring**: Automatic backend health checks with circuit breakers
- **Session Affinity**: Sticky sessions based on client IP or custom headers
- **Connection Migration**: QUIC connection migration support
- **Real-time Metrics**: Prometheus metrics with Grafana dashboards
- **Auto-scaling**: Easy horizontal scaling of backend services

## 🛠 Development

### Docker Commands
```bash
# Build and start all services
docker compose up --build -d

# View logs for specific service
docker compose logs -f quic-server
docker compose logs -f prometheus
docker compose logs -f grafana

# Restart specific service
docker compose restart quic-server

# Stop and remove everything
docker compose down

# Remove volumes too (warning: deletes data)
docker compose down -v

# Check service status
docker compose ps
```

### Project Structure
```
├── docker-compose.yml      # Main orchestration file
├── Dockerfile             # QUIC server container
├── .dockerignore          # Docker build optimization
├── main.go                # QUIC load balancer source
├── metrics.go             # Prometheus metrics
├── static/                # Static web content
├── monitoring/            # Prometheus & Grafana config
│   ├── prometheus.yml
│   └── grafana/
└── README.md              # This file
```

## 📊 Monitoring

Access the monitoring stack:
- **Prometheus**: http://localhost:9090 - View raw metrics and queries
- **Grafana**: http://localhost:3000 - Visual dashboards and alerts
- **Node Exporter**: http://localhost:9100 - System-level metrics

### Key Metrics
- Request latency and throughput
- Backend health and response times
- QUIC connection statistics
- Load balancer algorithm performance
- System resource utilization

## 🔧 Configuration

### Environment Variables
Modify `docker-compose.yml` to customize:
- `BACKEND_1_URL`, `BACKEND_2_URL`, `BACKEND_3_URL`: Backend endpoints
- `GRAFANA_ADMIN_PASSWORD`: Grafana admin password

### Scaling Backends
Add more backend services in `docker-compose.yml`:
```yaml
backend4:
  build:
    context: .
    dockerfile: Dockerfile.backend
  image: quic-backend:latest
  container_name: quic-backend4
  ports:
    - "8084:8000"
  networks:
    - quic-network
```

## 🧪 Testing

### Load Testing
```bash
# Test HTTP endpoint
curl http://localhost:8080

# Test HTTPS endpoint
curl -k https://localhost:9443

# Load test with multiple requests
for i in {1..100}; do curl -s http://localhost:8080 > /dev/null; done
```

### Health Checks
```bash
# Check service status
docker compose ps

# Check backend health
curl http://localhost:8080/health

# View metrics
curl http://localhost:9443/metrics
```

## 📝 Logs

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f quic-server
docker compose logs -f prometheus
docker compose logs -f grafana

# Follow logs in real-time
docker compose logs --tail=50 -f quic-server
```

## 🔄 Updates

After code changes:
```bash
# Rebuild and restart QUIC server
docker compose up --build -d quic-server

# Rebuild all services
docker compose down
docker compose up --build -d
```

## 🛡 Security

- TLS certificates included for HTTPS/QUIC
- Grafana authentication required
- Services isolated in Docker network
- No external database dependencies

## 📚 Learn More

- **QUIC Protocol**: [RFC 9000](https://tools.ietf.org/html/rfc9000)
- **HTTP/3**: [RFC 9114](https://tools.ietf.org/html/rfc9114)
- **Prometheus**: [prometheus.io](https://prometheus.io/)
- **Grafana**: [grafana.com](https://grafana.com/)

---

**Built with ❤️ using Go, Docker, Prometheus, and Grafana**
