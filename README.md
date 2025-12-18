# CTF Backend

A high-performance, concurrent Go backend for Capture The Flag (CTF) competitions.

## Features

- **Real-time leaderboard** with intelligent caching
- **Atomic flag validation** preventing race conditions
- **User progress tracking** across multiple challenge levels
- **Optimized performance** with prefetching and in-memory caching
- **Graceful shutdown** and signal handling

## Tech Stack

- **Go** - Backend server
- **MongoDB** - Database
- **CORS-enabled** REST API

## Prerequisites

- Go 1.16+
- MongoDB instance

## Setup

1. **Install dependencies**
   ```bash
   go mod download
   ```

2. **Set environment variables**
   ```bash
   export MONGODB_URI="your_mongodb_connection_string"
   export PORT=10000  # Optional, defaults to 10000
   ```

3. **Run the server**
   ```bash
   go run main.go
   ```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/test` | Health check |
| GET | `/getLevel?userId={id}` | Get user's current level |
| POST | `/checkFlag` | Submit a flag |
| POST | `/resetUser` | Reset user progress |
| POST | `/deleteUser` | Delete a user |
| GET | `/api/leaderboard` | Get top 100 players |
| GET | `/api/challenges` | List all challenges |

## Request Examples

### Check Flag
```bash
curl -X POST http://localhost:10000/checkFlag \
  -H "Content-Type: application/json" \
  -d '{"userId": "player1", "flag": "CTF{...}"}'
```

### Get Leaderboard
```bash
curl http://localhost:10000/api/leaderboard
```

## Performance Features

- **O(1) challenge lookups** via hash maps
- **User data prefetching** for reduced latency
- **Leaderboard caching** with background refresh
- **Database indexes** for optimized queries
- **Atomic operations** for thread-safety

## Development

Run with race detector:
```bash
go run -race main.go
```

Build for production:
```bash
go build -o ctf-server main.go
```

## License

MIT
