#!/usr/bin/env python3
"""
Minimal REST service for testing through Envoy proxy.
Serves simple endpoints using FastAPI.
Automatically registers with Consul on startup.
"""
import logging
import os
import socket
import tempfile
import ipaddress
from datetime import datetime, timedelta

import httpx
from contextlib import asynccontextmanager
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from fastapi import FastAPI, Query
from fastapi.responses import JSONResponse

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Consul configuration
CONSUL_HOST = os.getenv('CONSUL_HOST', "consul-agent")
CONSUL_PORT = os.getenv('CONSUL_PORT', 8500)
CONSUL_URL = f"http://{CONSUL_HOST}:{CONSUL_PORT}" if CONSUL_HOST else ""
CONSUL_REGISTER = os.getenv('CONSUL_REGISTER', "false").lower() == "true"
SERVICE_NAME = os.getenv('SERVICE_NAME', "hello-service")
SERVICE_PORT = int(os.getenv('SERVICE_PORT', 8080))
CONTAINER_NAME = os.getenv('CONTAINER_NAME', socket.gethostname())
SERVICE_ID = os.getenv('SERVICE_ID', f"{CONTAINER_NAME}:{SERVICE_PORT}")

async def register_with_consul():
    """Register this service with Consul."""
    registration = {
        "ID": SERVICE_ID,
        "Name": SERVICE_NAME,
        "Address": CONTAINER_NAME,
        "Port": SERVICE_PORT,
        "Meta": {
            "route_1_match_type": "path",
            "route_1_path_prefix": f"/{SERVICE_NAME}/",
            "route_1_prefix_rewrite": "/",
            "route_2_match_type": "header",
            "route_2_header_name": "X-Service",
            "route_2_header_value": SERVICE_NAME,
            "route_2_path_prefix": "/",
            "route_2_prefix_rewrite": "/",
            "route_3_match_type": "path",
            "route_3_path_prefix": f"/{SERVICE_NAME}-regex",
            "route_3_regex_rewrite": "^/{SERVICE_NAME}-regex(?:/(.*))?$",
            "route_3_regex_replacement": "/\\1"
        }
    }
    
    try:
        async with httpx.AsyncClient() as client:
            response = await client.put(
                f"{CONSUL_URL}/v1/agent/service/register",
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
                f"{CONSUL_URL}/v1/agent/service/deregister/{SERVICE_ID}",
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
    if CONSUL_REGISTER:
        await register_with_consul()
    yield
    if CONSUL_REGISTER:
        await deregister_from_consul()

app = FastAPI(lifespan=lifespan)

@app.get('/')
async def root():
    """Root endpoint."""
    return JSONResponse({'message': "REST service is running"}, status_code=200)

@app.get('/health')
async def health():
    """Health check endpoint."""
    return JSONResponse({'status': "ok"}, status_code=200)

@app.get('/hello')
async def hello(name: str = Query("World")):
    """Simple hello endpoint with optional name parameter."""
    message = f"Hello {name} from REST service"
    return JSONResponse({
        'message': message,
        'service': "rest",
        'name': name
    }, status_code=200)

@app.get('/info')
async def info():
    """Service info endpoint."""
    return JSONResponse({
        'name': "rest-service",
        'version': "1.0.0",
        'endpoints': ["/health", "/hello", "/info"]
    }, status_code=200)

def generate_self_signed_cert():
    """Generate a self-signed certificate and private key."""
    logger.info(f"Generating self-signed certificate for CN={CONTAINER_NAME}")

    # Generate private key
    key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
    )
    logger.debug("Generated 2048-bit RSA private key")

    # Generate certificate
    subject = issuer = x509.Name([
        x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
        x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, "Local"),
        x509.NameAttribute(NameOID.LOCALITY_NAME, "Local"),
        x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Test"),
        x509.NameAttribute(NameOID.COMMON_NAME, CONTAINER_NAME),
    ])

    cert = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(issuer)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(datetime.utcnow())
        .not_valid_after(datetime.utcnow() + timedelta(days=365))
        .add_extension(
            x509.SubjectAlternativeName([
                x509.DNSName("localhost"),
                x509.DNSName(CONTAINER_NAME),
                x509.IPAddress(ipaddress.IPv4Address("127.0.0.1")),
            ]),
            critical=False,
        )
        .sign(key, hashes.SHA256())
    )
    logger.debug(f"Generated certificate with SANs: localhost, {CONTAINER_NAME}, 127.0.0.1")

    # Write to temp files
    cert_file = tempfile.NamedTemporaryFile(delete=False, suffix=".pem")
    key_file = tempfile.NamedTemporaryFile(delete=False, suffix=".pem")

    cert_file.write(cert.public_bytes(serialization.Encoding.PEM))
    key_file.write(key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.TraditionalOpenSSL,
        encryption_algorithm=serialization.NoEncryption(),
    ))

    cert_file.close()
    key_file.close()

    logger.info(f"Certificate written to {cert_file.name}")
    logger.info(f"Private key written to {key_file.name}")

    return cert_file.name, key_file.name

if __name__ == "__main__":
    import uvicorn

    use_tls = os.getenv('USE_TLS', "false").lower() == "true"
    log_level = os.getenv('LOG_LEVEL', "info").lower()

    if use_tls:
        cert_path, key_path = generate_self_signed_cert()

        ssl_ciphers = ':'.join([
            'ECDHE-RSA-AES128-GCM-SHA256',
            'ECDHE-RSA-AES256-GCM-SHA384',
            'ECDHE-RSA-CHACHA20-POLY1305',
            'ECDHE-ECDSA-AES128-GCM-SHA256',
            'ECDHE-ECDSA-AES256-GCM-SHA384',
            'ECDHE-ECDSA-CHACHA20-POLY1305',
        ])

        config = uvicorn.Config(
            app,
            host='0.0.0.0',
            port=SERVICE_PORT,
            ssl_keyfile=key_path,
            ssl_certfile=cert_path,
            ssl_ciphers=ssl_ciphers,
            log_level=log_level,
        )
        server = uvicorn.Server(config)
        server.run()
    else:
        uvicorn.run(app, host='0.0.0.0', port=SERVICE_PORT, log_level=log_level)
