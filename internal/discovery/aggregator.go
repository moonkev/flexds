package discovery

import (
	"github.com/moonkev/flexds/internal/common/types"
	"github.com/moonkev/flexds/internal/xds"
)

type DiscoveredServiceAggregator struct {
	discoveredServiceMap map[string][]*types.DiscoveredService
	snapshotManager      *xds.SnapshotManager
}

func NewDiscoveredServiceAggregator(snapshotManager *xds.SnapshotManager) *DiscoveredServiceAggregator {
	return &DiscoveredServiceAggregator{
		discoveredServiceMap: make(map[string][]*types.DiscoveredService),
		snapshotManager:      snapshotManager,
	}
}

func (a *DiscoveredServiceAggregator) UpdateServices(loaderId string, services []*types.DiscoveredService) error {
	a.discoveredServiceMap[loaderId] = services

	aggregateLen := 0
	for _, svcList := range a.discoveredServiceMap {
		aggregateLen += len(svcList)
	}

	aggregatedServices := make([]*types.DiscoveredService, 0, aggregateLen)

	for _, svcList := range a.discoveredServiceMap {
		aggregateLen += len(svcList)
		aggregatedServices = append(aggregatedServices, svcList...)
	}

	a.snapshotManager.BuildAndPushSnapshot(aggregatedServices)
	return nil
}
