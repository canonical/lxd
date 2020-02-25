package db

func (c *ClusterTx) InstanceListExpanded() ([]Instance, error) {
	return c.instanceListExpanded()
}
