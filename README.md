# Turnip WebRTC Signaling Server

A secure WebRTC signaling server written in Go with JWT authentication and Redis-based multi-instance support. This server facilitates WebRTC peer-to-peer connections by handling signaling messages between clients across multiple server instances.

## Features

- **JWT Authentication**: Secure WebSocket connections using JWT tokens
- **Multi-Instance Support**: Redis-based distributed signaling across multiple server instances
- **Room-based Communication**: Clients can join specific rooms for isolated signaling
- **WebSocket Support**: Real-time bidirectional communication
- **Redis Pub/Sub**: Distributed messaging between server instances
- **Configurable**: Environment-based configuration
- **Logging**: Structured logging with zerolog
- **Health Checks**: Built-in health check endpoint
- **Graceful Shutdown**: Proper cleanup of resources and connections

## Architecture

The server supports multi-instance deployment where clients can connect to different server instances while still communicating within the same rooms. This is achieved through:

- **Redis Pub/Sub**: Messages are distributed across instances via Redis channels
- **Distributed State**: Client and room information is stored in Redis with TTL
- **Instance Identification**: Each server instance has a unique ID to prevent message loops
- **Automatic Cleanup**: Expired client sessions are automatically cleaned up

## Quick Start

### Prerequisites

- Go 1.21 or higher
- Redis 6.0 or higher
- Git

### Installation

1. Clone the repository:
```bash
git clone <your-repo-url>
cd turnip
```

2. Install dependencies:
```bash
make deps
```

3. Configure environment variables:
```bash
cp .env.example .env
# Edit .env with your configuration
```

### Running with Docker Compose (Recommended)

The easiest way to run the multi-instance setup is with Docker Compose:

```bash
# Start Redis and two signaling server instances
docker-compose up

# This will start:
# - Redis on port 6379
# - Signaling instance 1 on port 5004
# - Signaling instance 2 on port 5005
```

### Running Locally

1. Start Redis:
```bash
# Using Docker
docker run -d -p 6379:6379 redis:7-alpine

# Or install Redis locally
# macOS: brew install redis && brew services start redis
# Ubuntu: sudo apt install redis-server && sudo systemctl start redis
```

2. Run the server:
```bash
# Development mode with hot reload
make dev

# Or build and run
make build
./tmp/main
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `5004` | HTTP server port |
| `PUBLIC_IP` | `0.0.0.0` | Server bind address |
| `ACCESS_SECRET` | **Required** | JWT signing secret |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `REDIS_PASSWORD` | `` | Redis password (if required) |
| `REDIS_DB` | `0` | Redis database number |
| `INSTANCE_ID` | `hostname` | Unique instance identifier |
| `THREAD_NUM` | `2*CPU_COUNT` | Number of threads |
| `ENVIRONMENT` | `` | Environment (production for JSON logs) |
| `REALM` | `development` | Authentication realm for multi-tenant setups |
 