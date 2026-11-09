# UrbanFix

A small Go microservices project for booking home services. It is built to practice **microservice communication** with both **synchronous gRPC** and **asynchronous messaging via RabbitMQ**, backed by a shared PostgreSQL database.

## Architecture

```
                 HTTP :8001
   Client ──────────────────▶ ┌──────────────┐
                               │   Booking    │─── INSERT ───▶ ┌────────────┐
                               │   Service    │                 │ PostgreSQL │
                               └──────┬───────┘                 └─────▲──────┘
                                      │                               │
                     gRPC :50051      │  publish                      │ SELECT
                  CheckServiceExists  │  "booking.created"            │
                                      ▼                               │
   Client ──────────────────▶ ┌──────────────┐               ┌───────┴──────┐
        HTTP :8000             │   Catalog    │───────────────│   RabbitMQ   │
                               │   Service    │               │ booking.     │
                               └──────────────┘               │ created queue│
                                                              └───────┬──────┘
                                                                      │ consume
                                                              ┌───────▼──────┐
                                                              │ Notification │
                                                              │   Service    │
                                                              └──────────────┘
```

### Services

| Service | Port | Responsibility |
|---|---|---|
| **catalog** | HTTP `8000`, gRPC `50051` | Manages the list of services (e.g. "Plumbing"). Exposes a REST API and a gRPC API that lets other services verify a service exists. |
| **booking** | HTTP `8001` | Creates and lists bookings. Validates the service via gRPC call to catalog, saves the booking, then publishes a `booking.created` event. |
| **notification** | none | Consumes `booking.created` events from RabbitMQ and logs a notification (manual ack). |

### Communication patterns

- **Synchronous (gRPC, unary RPC):** `booking → catalog` via `CatalogService.CheckServiceExists`, with a 2 second timeout. If catalog is down, booking returns `503`.
- **Asynchronous (RabbitMQ):** `booking → notification` via a durable `booking.created` queue with persistent messages. The consumer uses manual acknowledgements.

## Tech Stack

- **Language:** Go
- **HTTP framework:** [Fiber v3](https://github.com/gofiber/fiber)
- **Database:** PostgreSQL 17 via [pgx v5](https://github.com/jackc/pgx) (`pgxpool`)
- **RPC:** gRPC + Protocol Buffers
- **Messaging:** RabbitMQ 4 via [amqp091-go](https://github.com/rabbitmq/amqp091-go)
- **Infra:** Docker Compose

## Project Structure

```
├── cmd
│   ├── booking
│   │   └── main.go          # Booking REST API, gRPC client, RabbitMQ publisher
│   ├── catalog
│   │   └── main.go          # Catalog REST API + gRPC server
│   └── notification
│       └── main.go          # RabbitMQ consumer
├── proto
│   ├── catalog.proto        # gRPC service definition
│   ├── catalog.pb.go        # Generated messages
│   └── catalog_grpc.pb.go   # Generated gRPC client/server
├── sql
│   └── schema.sql           # Tables: services, bookings
├── docker-compose.yml       # PostgreSQL + RabbitMQ
└── go.mod
```

## Prerequisites

- Go (see `go.mod` for the version)
- Docker and Docker Compose
- *(Optional, only to regenerate protobuf code)* `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`

## Getting Started

### 1. Start infrastructure

```bash
docker compose up -d
```

This starts:

- PostgreSQL on `localhost:5432` (user/password/db: `urbanfix`). `sql/schema.sql` is applied automatically on first start.
- RabbitMQ on `localhost:5672`, with the management UI at http://localhost:15672 (user/password: `urbanfix`).

### 2. Run the services

Start each in its own terminal, with **catalog first** since booking depends on it:

```bash
go run ./cmd/catalog
go run ./cmd/booking
go run ./cmd/notification
```

## API Reference

### Catalog service (`:8000`)

**List services**

```bash
curl http://localhost:8000/services
```

**Create a service**

```bash
curl -X POST http://localhost:8000/services \
  -H "Content-Type: application/json" \
  -d '{"name": "Plumbing"}'
```

Response `201`:

```json
{ "id": 1, "name": "Plumbing" }
```

### Booking service (`:8001`)

**Create a booking**

```bash
curl -X POST http://localhost:8001/bookings \
  -H "Content-Type: application/json" \
  -d '{"service_id": 1, "customer_name": "Rohit"}'
```

Response `201`:

```json
{
  "id": 1,
  "service_id": 1,
  "customer_name": "Rohit",
  "status": "pending",
  "created_at": "2026-11-07T10:00:00Z"
}
```

| Status | Meaning |
|---|---|
| `201` | Booking created and event published |
| `400` | Invalid body, or `service_id` does not exist in catalog |
| `503` | Catalog service unavailable (gRPC call failed) |
| `500` | Database or RabbitMQ error |

**List bookings**

```bash
curl http://localhost:8001/bookings
```

### gRPC (`:50051`)

```proto
service CatalogService {
  rpc CheckServiceExists (CheckServiceRequest) returns (CheckServiceResponse);
}
```

Test with [grpcurl](https://github.com/fullstorydev/grpcurl) (reflection is not enabled, so pass the proto file):

```bash
grpcurl -plaintext -proto proto/catalog.proto \
  -d '{"service_id": 1}' \
  localhost:50051 catalog.CatalogService/CheckServiceExists
```

## Try the Full Flow

1. Create a service in catalog (`POST :8000/services`).
2. Create a booking for it (`POST :8001/bookings`).
3. Watch the notification terminal print:

```
Notification for booking #1 created for Rohit (service id=1)
```

## Regenerating Protobuf Code

```bash
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/catalog.proto
```

## Database Schema

```sql
services (id BIGSERIAL PK, name TEXT)
bookings (id BIGSERIAL PK, service_id BIGINT, customer_name TEXT,
          status TEXT DEFAULT 'pending', created_at TIMESTAMPTZ DEFAULT NOW())
```

## Known Limitations / Roadmap

- [ ] Connection strings and ports are hardcoded; move them to environment variables
- [ ] Booking saves to the DB and publishes to RabbitMQ non-atomically (consider the transactional outbox pattern)
- [ ] No graceful shutdown for the HTTP, gRPC or consumer loops
- [ ] Notification service only logs; no real email/SMS delivery
- [ ] No tests or Dockerfiles for the Go services yet
- [ ] Booking status is always `pending`; no update flow yet
- [ ] `bookings.service_id` has no foreign key, since services are checked over gRPC

## Changelog

- **v0.1:** Catalog and Booking REST APIs, gRPC service check, RabbitMQ booking events, notification consumer, Docker Compose for Postgres and RabbitMQ.
