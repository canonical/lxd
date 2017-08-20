// +build go1.8

package query_test

var testStringsErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
	{"SELECT id, name FROM test", "query yields 2 columns, not 1"},
	{"SELECT id FROM test", "query yields INTEGER column, not TEXT"},
}

var testIntegersErrorCases = []struct {
	query string
	error string
}{
	{"garbage", "near \"garbage\": syntax error"},
	{"SELECT id, name FROM test", "query yields 2 columns, not 1"},
	{"SELECT name FROM test", "query yields TEXT column, not INTEGER"},
}
