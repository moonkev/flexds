package server

import (
	"context"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"time"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	clusterservice "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointservice "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenerservice "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routeservice "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
)

// RunGRPC starts the gRPC XDS server
func RunGRPC(ctx context.Context, adsServer serverv3.Server, port int) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("failed to listen on %d: %v", port, err)
	}

	// gRPC server options for better streaming support
	grpcOptions := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(1000000),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	grpcServer := grpc.NewServer(grpcOptions...)

	// Register all the discovery service servers on the gRPC server
	discovery.RegisterAggregatedDiscoveryServiceServer(grpcServer, adsServer)
	clusterservice.RegisterClusterDiscoveryServiceServer(grpcServer, adsServer)
	endpointservice.RegisterEndpointDiscoveryServiceServer(grpcServer, adsServer)
	listenerservice.RegisterListenerDiscoveryServiceServer(grpcServer, adsServer)
	routeservice.RegisterRouteDiscoveryServiceServer(grpcServer, adsServer)

	log.Printf("[GRPC] registered all discovery services with keepalive on port %d", port)

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("[GRPC] ADS server listening on %d", port)
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		log.Printf("[GRPC] context cancelled, stopping gRPC server")
		grpcServer.GracefulStop()
		log.Printf("[GRPC] waiting for server to stop...")
		<-serveErr
		log.Printf("[GRPC] gRPC server stopped via context")
	case err := <-serveErr:
		log.Fatalf("[GRPC] serve error: %v", err)
	}
}

// ServerCallbacks implements the Callbacks interface for logging client events
type ServerCallbacks struct{}

func (cb *ServerCallbacks) OnStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	log.Printf("[STREAM OPEN] streamID=%d typeURL=%s - callback fired", streamID, typeURL)
	return nil
}

func (cb *ServerCallbacks) OnStreamClosed(streamID int64, node *core.Node) {
	log.Printf("[STREAM CLOSED] streamID=%d nodeID=%s", streamID, node.Id)
}

func (cb *ServerCallbacks) OnStreamRequest(streamID int64, req *discovery.DiscoveryRequest) error {
	log.Printf("[STREAM REQUEST] streamID=%d nodeID=%s typeURL=%s resourceNames=%v responseNonce=%s versionInfo=%s",
		streamID, req.Node.Id, req.TypeUrl, req.ResourceNames, req.ResponseNonce, req.VersionInfo)

	// Debug: indicate we received the request and the server should look up resources
	log.Printf("[STREAM REQUEST DEBUG] server should now retrieve resources for nodeID=%s typeURL=%s from cache", req.Node.Id, req.TypeUrl)
	return nil
}

func (cb *ServerCallbacks) OnStreamResponse(ctx context.Context, streamID int64, req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
	if resp != nil {
		log.Printf("[STREAM RESPONSE] streamID=%d nodeID=%s typeURL=%s resources=%d nonce=%s version=%s",
			streamID, req.Node.Id, req.TypeUrl, len(resp.Resources), resp.Nonce, resp.VersionInfo)
	} else {
		log.Printf("[STREAM RESPONSE] streamID=%d nodeID=%s typeURL=%s response=nil",
			streamID, req.Node.Id, req.TypeUrl)
	}
}

func (cb *ServerCallbacks) OnFetchRequest(ctx context.Context, req *discovery.DiscoveryRequest) error {
	log.Printf("[FETCH REQUEST] nodeID=%s typeURL=%s resourceNames=%v",
		req.Node.Id, req.TypeUrl, req.ResourceNames)
	return nil
}

func (cb *ServerCallbacks) OnFetchResponse(req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
	log.Printf("[FETCH RESPONSE] nodeID=%s typeURL=%s resources=%d",
		req.Node.Id, req.TypeUrl, len(resp.Resources))
}

func (cb *ServerCallbacks) OnDeltaStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	log.Printf("[DELTA STREAM OPEN] streamID=%d typeURL=%s", streamID, typeURL)
	return nil
}

func (cb *ServerCallbacks) OnDeltaStreamClosed(streamID int64, node *core.Node) {
	log.Printf("[DELTA STREAM CLOSED] streamID=%d nodeID=%s", streamID, node.Id)
}

func (cb *ServerCallbacks) OnStreamDeltaRequest(streamID int64, req *discovery.DeltaDiscoveryRequest) error {
	log.Printf("[DELTA REQUEST] streamID=%d nodeID=%s typeURL=%s", streamID, req.Node.Id, req.TypeUrl)
	return nil
}

func (cb *ServerCallbacks) OnStreamDeltaResponse(streamID int64, req *discovery.DeltaDiscoveryRequest, resp *discovery.DeltaDiscoveryResponse) {
	log.Printf("[DELTA RESPONSE] streamID=%d nodeID=%s typeURL=%s", streamID, req.Node.Id, resp.TypeUrl)
}
