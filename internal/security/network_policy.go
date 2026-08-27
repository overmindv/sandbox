package security

type NetworkPolicy struct {
	AllowUserCodeNetwork bool
	DenyMetadataEndpoint bool
}

func DefaultNetworkPolicy() NetworkPolicy {
	return NetworkPolicy{
		AllowUserCodeNetwork: false,
		DenyMetadataEndpoint: true,
	}
}
