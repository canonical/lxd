// +build !linux

package i18n

func G(msgid string) string {
	return msgid
}

func NG(msgid string, msgidPlural string, n uint64) string {
	return msgid
}
