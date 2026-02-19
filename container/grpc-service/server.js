const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');
const http = require('http');
const os = require('os');

// Load proto definition
const packageDef = protoLoader.loadSync(path.join(__dirname, 'streaming.proto'), {});
const proto = grpc.loadPackageDefinition(packageDef);

// Consul configuration
const CONSUL_HOST = process.env.CONSUL_HOST || 'consul-agent';
const CONSUL_PORT = process.env.CONSUL_PORT || 8500;
const SERVICE_NAME = process.env.SERVICE_NAME || 'grpc-service';
const CONSUL_REGISTER = process.env.CONSUL_REGISTER?.toLowerCase() === 'true';
const SERVICE_PORT = 9090;
const CONTAINER_NAME = process.env.CONTAINER_NAME || os.hostname();
const SERVICE_ID = process.env.SERVICE_ID || `${CONTAINER_NAME}:${SERVICE_PORT}`;

// Implement service methods
const services = {
  CounterStream: (call) => {
    const start = call.request.start || 0;
    const count = call.request.count || 10;

    console.log(`CounterStream called: start=${start}, count=${count}`);

    for (let i = 0; i < count; i++) {
      call.write({
        value: start + i,
        message: `counting... ${start + i}`
      });
    }
    call.end();
  },

  Health: (call, callback) => {
    console.log('Health check called');
    callback(null, { status: 'ok' });
  }
};

// Consul registration function
function registerWithConsul() {
  const registration = {
    ID: SERVICE_ID,
    Name: SERVICE_NAME,
    Address: CONTAINER_NAME,
    Port: SERVICE_PORT,
    Meta: {
      "http2": "true",
      "route_1_match_type": "header",
      "route_1_header_name": "X-Service",
      "route_1_header_value": "grpc-service",
      "route_1_path_prefix": "/",
      "route_1_rewrite_prefix": "/"
    }
  };

  const postData = JSON.stringify(registration);
  
  const options = {
    hostname: CONSUL_HOST,
    port: CONSUL_PORT,
    path: '/v1/agent/service/register',
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json',
      'Content-Length': Buffer.byteLength(postData)
    }
  };

  const req = http.request(options, (res) => {
    let data = '';
    res.on('data', (chunk) => { data += chunk; });
    res.on('end', () => {
      if (res.statusCode === 200) {
        console.log(`Successfully registered ${SERVICE_ID} with Consul`);
      } else {
        console.error(`Failed to register with Consul: ${res.statusCode} ${data}`);
      }
    });
  });

  req.on('error', (e) => {
    console.error(`Error registering with Consul: ${e.message}`);
  });

  req.write(postData);
  req.end();
}

// Create and start server
const server = new grpc.Server();
server.addService(proto.grpc_streaming.StreamingService.service, services);

const PORT = '0.0.0.0:9090';
server.bindAsync(PORT, grpc.ServerCredentials.createInsecure(), (err, port) => {
  if (err) {
    console.error('Failed to bind server:', err);
    process.exit(1);
  }
  server.start();
  console.log(`gRPC server listening on ${PORT}`);
  
  // Register with Consul after server starts
  if (CONSUL_REGISTER){
    registerWithConsul();
  }
});

// Graceful shutdown
process.on('SIGTERM', () => {
  console.log('SIGTERM received, shutting down gracefully');
  server.tryShutdown(() => {
    console.log('Server shutdown complete');
    process.exit(0);
  });
});
