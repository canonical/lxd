package request

// UserAgentNotifier used to distinguish between a regular client request and an internal cluster request when
// notifying other nodes of a cluster change.
const UserAgentNotifier = "lxd-cluster-notifier"

// UserAgentJoiner used to distinguish between a regular client request and an internal cluster request when
// joining a node to a cluster.
const UserAgentJoiner = "lxd-cluster-joiner"

// UserAgentOperationNotifier is used to distinguish between a standard internal cluster request (which uses UserAgentNotifier)
// and an internal cluster request coming from within an operation (which user UserAgentOperationNotifier). The notified node
// does not need to create another operation to handle the request in this case, as the asynchronous nature is already achieved
// by the operation on the sending node.
const UserAgentOperationNotifier = "lxc-operation-notifier"

// ClientType indicates which sort of client type is being used.
type ClientType string

// ClientTypeNotifier cluster notification client.
const ClientTypeNotifier ClientType = "notifier"

// ClientTypeJoiner cluster joiner client.
const ClientTypeJoiner ClientType = "joiner"

// ClientTypeNormal normal client.
const ClientTypeNormal ClientType = "normal"

// ClientTypeOperationNotifier cluster notification client coming from within an operation.
const ClientTypeOperationNotifier ClientType = "operation-notifier"

// UserAgentClientType converts user agent to client type.
func userAgentClientType(userAgent string) ClientType {
	switch userAgent {
	case UserAgentNotifier:
		return ClientTypeNotifier
	case UserAgentOperationNotifier:
		return ClientTypeOperationNotifier
	case UserAgentJoiner:
		return ClientTypeJoiner
	}

	return ClientTypeNormal
}

// IsClusterNotification returns true if the ClientType is ClientTypeNotifier.
func (c ClientType) IsClusterNotification() bool {
	return c == ClientTypeNotifier
}

// IsClusterOperationNotification returns true if the ClientType is ClientTypeOperationNotifier.
func (c ClientType) IsClusterOperationNotification() bool {
	return c == ClientTypeOperationNotifier
}
