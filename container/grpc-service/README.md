# gRPC Streaming Service

Node.js-based gRPC service for testing streaming RPC methods through Envoy proxy.

## Proto Definition

See `streaming.proto` for the service and message definitions.

## Endpoints

### Unary RPC
- `Health` — Returns health status

### Server-Streaming RPC
- `CounterStream(start, count)` — Streams integers from `start` to `start+count`

## Local Development

```bash
npm install
node server.js
```

## Docker

Build:
```bash
docker build -t grpc-service:latest -f Dockerfile .
```

Run:
```bash
docker run -p 9090:9090 grpc-service:latest
```

## Testing with grpcurl

```bash
# List services
grpcurl -plaintext localhost:9090 list

# Call Health
grpcurl -plaintext localhost:9090 grpc_streaming.StreamingService/Health

# Call CounterStream (server streaming)
grpcurl -plaintext -d '{"start": 5, "count": 10}' localhost:9090 grpc_streaming.StreamingService/CounterStream
```
