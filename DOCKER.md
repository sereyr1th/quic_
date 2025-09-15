# Minimal Docker Setup

## Essential Files (3)
- `docker-compose.yml` - Orchestration
- `Dockerfile` - QUIC server build
- `.dockerignore` - Build optimization

## Quick Commands

```bash
# Start everything
docker compose up -d

# Stop everything
docker compose down

# View logs
docker compose logs -f

# Rebuild after changes
docker compose up --build -d

# Check status
docker compose ps
```

## Services
- QUIC Load Balancer: https://localhost:9443 & http://localhost:8080
- Backends: http://localhost:8081-8083
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (admin/admin123)
- Node Exporter: http://localhost:9100
