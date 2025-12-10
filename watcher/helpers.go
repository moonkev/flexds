package watcher

// filterServices extracts service names from the Consul response, excluding "consul"
func filterServices(services map[string][]string) []string {
	svcList := make([]string, 0)
	for name := range services {
		if name != "consul" {
			svcList = append(svcList, name)
		}
	}
	return svcList
}
