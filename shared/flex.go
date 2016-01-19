/* This is a FLEXible file which can be used by both client and daemon.
 * Teehee.
 */
package shared

var Version = "0.27"
var UserAgent = "LXD " + Version

/*
 * Please increment the api compat number every time you change the API.
 *
 * Version 1.0: ping
 */
var APICompat = 1
var APIVersion = "1.0"
