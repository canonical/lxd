// That this code needs to exist is kind of dumb, but I'm not sure how else to
// do it.
package shared

type StringSet map[string]bool

func (ss StringSet) IsSubset(oss StringSet) bool {
	for k, _ := range map[string]bool(ss) {
		if _, ok := map[string]bool(oss)[k]; !ok {
			return false
		}
	}

	return true
}

func NewStringSet(strings []string) StringSet {
	ret := map[string]bool{}
	for _, s := range strings {
		ret[s] = true
	}

	return StringSet(ret)
}
