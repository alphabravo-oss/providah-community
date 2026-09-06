package provider

// CommunityAction is the native implementation boundary, independent of worker claims.
func CommunityAction(cloud, kind string) bool {
	return cloud != "aws" || (!DatabaseKind(kind) && !DatabaseSnapshotKind(kind) && kind != "network.load_balancer")
}

func CommunityRuntime(r Runtime) Runtime {
	if r.Provider == "aws" {
		r.DatabasePower = false
		r.DatabaseSnapshotDelete = false
		r.LoadBalancerDelete = false
	}
	return r
}
