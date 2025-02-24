package internal

var (
	// TODO: this is manually hardcoded,
	// Maybe should be generated from the CRD
	SkyClusterAPI            = "skycluster.io"
	SkyClusterName           = "skycluster"
	SkyClusterVersion        = "v1alpha1"
	SkyClusterCoreGroup      = "core." + SkyClusterAPI
	SkyClusterManagedBy      = SkyClusterAPI + "/managed-by"
	SkyClusterManagedByValue = SkyClusterName
	SkyClusterConfigType     = SkyClusterAPI + "/config-type"
)
