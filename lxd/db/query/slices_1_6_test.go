// +build !go1.8

package query_test

var testStringsErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
	{"SELECT id, name FROM test", "sql: expected 2 destination arguments in Scan, not 1"},
}

var testIntegersErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
}
