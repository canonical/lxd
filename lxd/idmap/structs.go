package idmap

// IdmapEntry is a single idmap entry (line).
type IdmapEntry struct {
	Isuid    bool  `json:"Isuid"`
	Isgid    bool  `json:"Isgid"`
	Hostid   int64 `json:"Hostid"` // id as seen on the host - i.e. 100000
	Nsid     int64 `json:"Nsid"`   // id as seen in the ns - i.e. 0
	Maprange int64 `json:"Maprange"`
}

// IdmapSet is a list of IdmapEntry with some functions on it.
type IdmapSet struct {
	Idmap []IdmapEntry `json:"Idmap"`
}
