package request

// UserAgentNotifier used to distinguish between a regular client request and an internal cluster request when
// notifying other nodes of a cluster change.
const UserAgentNotifier = "lxd-cluster-notifier"

// UserAgentJoiner used to distinguish between a regular client request and an internal cluster request when
// joining a node to a cluster.
const UserAgentJoiner = "lxd-cluster-joiner"

// UserAgentDelegator used to distinguish between a regular client request and a delegation request when
// creating a cluster link.
const UserAgentDelegator = "lxd-cluster-delegator"

// ClientType indicates which sort of client type is being used.
type ClientType string

// ClientTypeNotifier cluster notification client.
const ClientTypeNotifier ClientType = "notifier"

// ClientTypeJoiner cluster joiner client.
const ClientTypeJoiner ClientType = "joiner"

// ClientTypeNormal normal client.
const ClientTypeNormal ClientType = "normal"

// ClientTypeDelegator cluster delegator client.
const ClientTypeDelegator ClientType = "delegator"

// UserAgentClientType converts user agent to client type.
func UserAgentClientType(userAgent string) ClientType {
	switch userAgent {
	case UserAgentNotifier:
		return ClientTypeNotifier
	case UserAgentJoiner:
		return ClientTypeJoiner
	case UserAgentDelegator:
		return ClientTypeDelegator
	}

	return ClientTypeNormal
}
