# QUIC Load Balancer Monitoring

This setup provides comprehensive monitoring for your QUIC HTTP/3 load balancer using Prometheus and Grafana.

## 🚀 Quick Start

1. **Start the monitoring stack:**
   ```bash
   ./start-monitoring.sh
   ```

2. **Access the dashboards:**
   - Grafana: http://localhost:3000 (admin/admin123)
   - Prometheus: http://localhost:9090
   - QUIC App: https://localhost:9443

## 📊 What We Monitor

### Connection Metrics
- **QUIC Connections**: Active connections, connection migrations
- **Network Performance**: RTT, bandwidth, congestion window
- **Packet Loss**: Real-time packet loss tracking
- **Path Validation**: Multi-path validation events

### Load Balancer Metrics
- **Backend Health**: Health status of all backends
- **Request Distribution**: How requests are distributed across backends
- **Response Times**: Latency metrics per backend
- **Circuit Breaker**: Circuit breaker states and failures

### Application Performance
- **Request Rate**: Requests per second by protocol (HTTP/1.1 vs HTTP/3)
- **Error Rate**: 4xx/5xx error tracking
- **Migration Success**: Connection migration success rates

## 🎯 Key Benefits for Moodle Deployment

### 1. **Performance Comparison**
- Side-by-side comparison of HTTP/1.1 vs HTTP/3 performance
- Real-time latency improvements visualization
- Bandwidth efficiency tracking

### 2. **Connection Migration Monitoring**
- Track how often students' connections migrate (WiFi to mobile, etc.)
- Monitor migration success rates
- Alert on migration failures

### 3. **Backend Health**
- Monitor Moodle server health
- Automatic failover detection
- Load distribution optimization

### 4. **Network Quality Insights**
- Packet loss tracking (important for mobile users)
- RTT monitoring (latency sensitive operations)
- Congestion window analysis

## 📈 Key Dashboards

### QUIC Load Balancer Overview
- **Request Rate**: Real-time request throughput
- **Active Connections**: Current active QUIC connections
- **Connection Migrations**: Migration events and success rates
- **Backend Health**: Health status of all Moodle backends
- **Response Time**: 95th and 50th percentile response times
- **Packet Loss**: Network quality metrics
- **RTT**: Round-trip time tracking

## 🔔 Alerting Rules

The system includes pre-configured alerts for:

- **High Migration Rate**: > 0.1 migrations/second
- **Backend Down**: Any backend becomes unhealthy
- **High Error Rate**: > 5% error rate
- **High Packet Loss**: > 2% packet loss
- **Circuit Breaker Open**: Any circuit breaker triggers

## 🛠️ Manual Operations

### View Metrics Directly
```bash
# Check if metrics are being collected
curl -k https://localhost:9443/metrics

# View specific metric
curl -k https://localhost:9443/metrics | grep quic_connections_total
```

### Restart Monitoring Stack
```bash
docker-compose -f docker-compose.monitoring.yml down
docker-compose -f docker-compose.monitoring.yml up -d
```

### Add Custom Metrics
Edit `metrics.go` to add new Prometheus metrics, then rebuild:
```bash
go build -o quic-moodle main.go metrics.go
```

## 🎯 Recommended for Moodle

### Before Deployment
1. **Baseline Metrics**: Run for 1-2 weeks with HTTP/1.1 to establish baseline
2. **Load Testing**: Use the monitoring to validate load balancer behavior
3. **Network Testing**: Test connection migrations in various network conditions

### During Rollout
1. **A/B Testing**: Monitor HTTP/1.1 vs HTTP/3 performance side-by-side
2. **User Experience**: Track latency improvements for different user groups
3. **Problem Detection**: Early warning system for performance issues

### Post-Deployment
1. **Performance Optimization**: Use metrics to fine-tune load balancing
2. **Capacity Planning**: Monitor trends for infrastructure scaling
3. **User Analytics**: Understand usage patterns and network conditions

## 🔧 Configuration

### Prometheus Configuration
- Scrape interval: 15 seconds
- Metrics retention: 200 hours
- QUIC app metrics: Every 5 seconds

### Grafana Configuration
- Auto-refresh: 5 seconds
- Data source: Prometheus
- Default credentials: admin/admin123

### Application Metrics
- Update interval: 10 seconds
- Connection cleanup: 1 minute
- Health checks: 15 seconds

## 📱 Mobile Considerations

The monitoring is especially valuable for Moodle's mobile users:

1. **Connection Migration**: Track WiFi ↔ Mobile network transitions
2. **Battery Impact**: Monitor connection efficiency
3. **Network Quality**: Track performance across different carriers
4. **Offline Resilience**: Monitor connection recovery patterns

## 🚨 Troubleshooting

### Common Issues

1. **No metrics appearing**: Check if `/metrics` endpoint is accessible
2. **Grafana connection issues**: Verify Prometheus is running on port 9090
3. **Docker issues**: Ensure Docker has sufficient resources allocated

### Debug Commands
```bash
# Check container status
docker-compose -f docker-compose.monitoring.yml ps

# View logs
docker-compose -f docker-compose.monitoring.yml logs prometheus
docker-compose -f docker-compose.monitoring.yml logs grafana

# Test metrics endpoint
curl -k https://localhost:9443/metrics | head -20
```

This monitoring setup gives you the visibility needed to confidently deploy QUIC/HTTP3 for Moodle while maintaining excellent user experience!
