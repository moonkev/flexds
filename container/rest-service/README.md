# REST Service

Simple Flask-based REST service for testing through Envoy proxy.

## Endpoints

- `GET /health` — Health check
- `GET /api/v1/hello` — Simple hello message
- `GET /api/v1/info` — Service information

## Local Development

```bash
pip install flask
python app.py
```

Then test:
```bash
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/hello
```

## Docker

Build:
```bash
docker build -t rest-service:latest -f Dockerfile .
```

Run:
```bash
docker run -p 8080:8080 rest-service:latest
```
