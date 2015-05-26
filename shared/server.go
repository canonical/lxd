package shared

type ServerState struct {
	APICompat   int                    `json:"api_compat"`
	Auth        string                 `json:"auth"`
	Environment map[string]string      `json:"environment"`
	Config      map[string]interface{} `json:"config"`
}
