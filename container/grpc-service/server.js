const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');

// Load proto definition
const packageDef = protoLoader.loadSync(path.join(__dirname, 'streaming.proto'), {});
const proto = grpc.loadPackageDefinition(packageDef);

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
});

// Graceful shutdown
process.on('SIGTERM', () => {
  console.log('SIGTERM received, shutting down gracefully');
  server.tryShutdown(() => {
    console.log('Server shutdown complete');
    process.exit(0);
  });
});
