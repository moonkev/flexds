package xds

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
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
		slog.Error("Failed to listen", "port", port, "error", err)
		os.Exit(1)
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

	slog.Info("registered all discovery services with keepalive", "port", port)

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("ADS server listening", "port", port)
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		slog.Info("context cancelled, stopping gRPC server")
		grpcServer.GracefulStop()
		slog.Info("waiting for server to stop")
		<-serveErr
		slog.Info("gRPC server stopped via context")
	case err := <-serveErr:
		slog.Error("serve error", "error", err)
		os.Exit(1)
	}
}

// ServerCallbacks implements the Callbacks interface for logging client events
type ServerCallbacks struct {
	serverv3.CallbackFuncs
	Cache cachev3.SnapshotCache
}

func (cb *ServerCallbacks) OnStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	slog.Debug("OnStreamOpen", "streamID", streamID, "typeURL", typeURL)
	return nil
}

func (cb *ServerCallbacks) OnStreamClosed(streamID int64, node *core.Node) {
	slog.Debug("OnStreamClosed", "streamID", streamID, "nodeID", node.Id)
}

func (cb *ServerCallbacks) OnStreamRequest(streamID int64, req *discovery.DiscoveryRequest) error {
	slog.Debug("OnStreamRequest",
		"streamID", streamID,
		"nodeID", req.Node.Id,
		"typeURL", req.TypeUrl,
		"resourceNames", req.ResourceNames,
		"responseNonce", req.ResponseNonce,
		"versionInfo", req.VersionInfo)
	snapshot, err := cb.Cache.GetSnapshot("__REFERENCE_SNAPSHOT__")
	if err != nil {
		slog.Error("error fetching reference snapshot", "error", err)
		return err
	}
	err = cb.Cache.SetSnapshot(context.Background(), req.Node.Id, snapshot)
	if err != nil {
		slog.Error("error setting snapshot for node", "nodeID", req.Node.Id, "error", err)
		return err
	}
	return nil
}

func (cb *ServerCallbacks) OnStreamResponse(ctx context.Context, streamID int64, req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
	if resp != nil {
		slog.Debug("OnStreamResponse",
			"streamID", streamID,
			"nodeID", req.Node.Id,
			"typeURL", req.TypeUrl,
			"resources", len(resp.Resources),
			"nonce", resp.Nonce,
			"version", resp.VersionInfo)
	} else {
		slog.Debug("OnStreamResponse (nil)", "streamID", streamID, "nodeID", req.Node.Id, "typeURL", req.TypeUrl)
	}
}

func (cb *ServerCallbacks) OnDeltaStreamOpen(ctx context.Context, streamID int64, typeURL string) error {
	slog.Debug("OnDeltaStreamOpen", "streamID", streamID, "typeURL", typeURL)
	return nil
}

func (cb *ServerCallbacks) OnDeltaStreamClosed(streamID int64, node *core.Node) {
	slog.Debug("OnDeltaStreamClosed", "streamID", streamID, "nodeID", node.Id)
}

func (cb *ServerCallbacks) OnStreamDeltaRequest(streamID int64, req *discovery.DeltaDiscoveryRequest) error {
	slog.Debug("OnStreamDeltaRequest", "streamID", streamID, "nodeID", req.Node.Id, "typeURL", req.TypeUrl)
	return nil
}

func (cb *ServerCallbacks) OnStreamDeltaResponse(streamID int64, req *discovery.DeltaDiscoveryRequest, resp *discovery.DeltaDiscoveryResponse) {
	slog.Debug("OnStreamDeltaResponse", "streamID", streamID, "nodeID", req.Node.Id, "typeURL", resp.TypeUrl)
}
