#!/usr/bin/env python3
"""
Minimal REST service for testing through Envoy proxy.
Serves simple endpoints using FastAPI.
Automatically registers with Consul on startup.
"""
import logging
import os
import socket
import httpx
from contextlib import asynccontextmanager
from fastapi import FastAPI, Query
from fastapi.responses import JSONResponse

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Consul configuration
CONSUL_HOST = os.getenv('CONSUL_HOST', 'consul-agent')
CONSUL_PORT = int(os.getenv('CONSUL_PORT', 8500))
CONSUL_URL = f'http://{CONSUL_HOST}:{CONSUL_PORT}'
SERVICE_NAME = os.getenv('SERVICE_NAME', 'hello-service')
SERVICE_PORT = int(os.getenv('SERVICE_PORT', 8080))
SERVICE_ID = os.getenv('SERVICE_ID', f'{SERVICE_NAME}:{SERVICE_PORT}')
CONTAINER_NAME = os.getenv('HOSTNAME', socket.gethostname())

async def register_with_consul():
    """Register this service with Consul."""
    registration = {
        "ID": SERVICE_ID,
        "Name": SERVICE_NAME,
        "Address": CONTAINER_NAME,
        "Port": SERVICE_PORT,
        "Meta": {
            "route_1_match_type": "path",
            "route_1_path_prefix": "/hello-service/",
            "route_1_prefix_rewrite": "/",
            "route_2_match_type": "header",
            "route_2_header_name": "X-Service",
            "route_2_header_value": "hello-service",
            "route_2_path_prefix": "/",
            "route_2_prefix_rewrite": "/",
            "route_3_match_type": "path",
            "route_3_path_prefix": "/regex-service",
            "route_3_regex_rewrite": "^/regex-service(?:/(.*))?$",
            "route_3_regex_replacement": "/\\1"
        }
    }
    
    try:
        async with httpx.AsyncClient() as client:
            response = await client.put(
                f'{CONSUL_URL}/v1/agent/service/register',
                json=registration,
                timeout=5
            )
            if response.status_code == 200:
                logger.info(f"Successfully registered {SERVICE_ID} with Consul")
            else:
                logger.error(f"Failed to register with Consul: {response.status_code} {response.text}")
    except Exception as e:
        logger.error(f"Error registering with Consul: {e}")

async def deregister_from_consul():
    """Deregister this service from Consul on shutdown."""
    try:
        async with httpx.AsyncClient() as client:
            response = await client.put(
                f'{CONSUL_URL}/v1/agent/service/deregister/{SERVICE_ID}',
                timeout=5
            )
            if response.status_code == 200:
                logger.info(f"Successfully deregistered {SERVICE_ID} from Consul")
    except Exception as e:
        logger.error(f"Error deregistering from Consul: {e}")

@asynccontextmanager
async def lifespan(app: FastAPI):
    """
    Manage service startup and shutdown.
    Register on startup, deregister on shutdown.
    """
    logger.info(f"Starting REST service on port {SERVICE_PORT}")
    await register_with_consul()
    yield
    await deregister_from_consul()

app = FastAPI(lifespan=lifespan)

@app.get('/')
async def root():
    """Root endpoint."""
    return JSONResponse({'message': 'REST service is running'}, status_code=200)

@app.get('/health')
async def health():
    """Health check endpoint."""
    return JSONResponse({'status': 'ok'}, status_code=200)

@app.get('/hello')
async def hello(name: str = Query('World')):
    """Simple hello endpoint with optional name parameter."""
    message = f'Hello {name} from REST service'
    return JSONResponse({
        'message': message,
        'service': 'rest',
        'name': name
    }, status_code=200)

@app.get('/info')
async def info():
    """Service info endpoint."""
    return JSONResponse({
        'name': 'rest-service',
        'version': '1.0.0',
        'endpoints': ['/health', '/hello', '/info']
    }, status_code=200)

if __name__ == '__main__':
    import uvicorn
    uvicorn.run(app, host='0.0.0.0', port=SERVICE_PORT)
