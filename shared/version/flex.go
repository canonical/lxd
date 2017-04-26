package version

// Version contains the LXD version number
var Version = "2.13"

// UserAgent contains a string suitable as a user-agent
var UserAgent = "LXD " + Version

// APIVersion contains the API base version. Only bumped for backward incompatible changes.
var APIVersion = "1.0"
