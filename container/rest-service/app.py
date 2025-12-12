#!/usr/bin/env python3
"""
Minimal REST service for testing through Envoy proxy.
Serves simple endpoints on port 8080.
"""
import json
import logging
from flask import Flask, jsonify, request

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = Flask(__name__)

@app.route('/health', methods=['GET'])
def health():
    """Health check endpoint."""
    return jsonify({'status': 'ok'}), 200

@app.route('/hello', methods=['GET'])
def hello():
    """Simple hello endpoint with optional name parameter."""
    name = request.args.get('name', 'World')
    message = f'Hello {name} from REST service'
    return jsonify({'message': message, 'service': 'rest', 'name': name}), 200

@app.route('/info', methods=['GET'])
def info():
    """Service info endpoint."""
    return jsonify({
        'name': 'rest-service',
        'version': '1.0.0',
        'endpoints': ['/health', '/hello', '/info']
    }), 200

if __name__ == '__main__':
    logger.info("Starting REST service on port 8080")
    app.run(host='0.0.0.0', port=8080, debug=False)
