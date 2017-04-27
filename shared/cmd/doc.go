/*

The package cmd implements a simple abstraction around a "sub-command" for
a main executable (e.g. "lxd init", where "init" is the sub-command).

It is designed to make unit-testing easier, since OS-specific parts like
standard in/out can be set in tests.

*/

package cmd
