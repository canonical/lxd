package storage

var lxdEarlyPatches = map[string]func(b *lxdBackend) error{}

var lxdLatePatches = map[string]func(b *lxdBackend) error{}
