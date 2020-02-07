package config

// ProxyAddress represents a proxy address configuration.
type ProxyAddress struct {
	ConnType string
	Addr     []string
	Abstract bool
}
