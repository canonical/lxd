// +build linux

package i18n

import (
	"github.com/gosexy/gettext"
)

var TEXTDOMAIN = "lxd"

func G(msgid string) string {
	return gettext.DGettext(TEXTDOMAIN, msgid)
}

func NG(msgid string, msgidPlural string, n uint64) string {
	return gettext.DNGettext(TEXTDOMAIN, msgid, msgidPlural, n)
}

func init() {
	gettext.SetLocale(gettext.LC_ALL, "")
}
